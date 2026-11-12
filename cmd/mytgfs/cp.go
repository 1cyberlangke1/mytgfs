package main

// `mytgfs cp` 子命令：频道内直接复制文件。
//
// 复制不下载也不重新上传字节：发一条引用同一 document 的新消息，
// 只换 caption 里的路径。源文件不动，目标路径的旧消息（如存在）会被删掉。
//
// 用法：
//   mytgfs cp [--name mytgfs] [--force] <src> <dst>
// 输入：src 源路径（卷内绝对路径，或以 / 开头的相对写法均可）
// 输出：COPY / OVERWRITE 行；失败退出 1
// 预期：src 必须在索引里；dst 已存在时默认拒绝，--force 才覆盖

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path"
	"strings"

	"mytgfs/internal/config"
	"mytgfs/internal/crypto"
	"mytgfs/internal/datadir"
	"mytgfs/internal/i18n"
	"mytgfs/internal/index"
	"mytgfs/internal/sessionstore"
	tgsync "mytgfs/internal/sync"
	"mytgfs/internal/telegramstore"
	"mytgfs/internal/tgclient"
)

// runCopy 处理频道内复制。
func runCopy(args []string) {
	fs := flag.NewFlagSet("cp", flag.ExitOnError)
	name := fs.String("name", "mytgfs", "volume name")
	force := fs.Bool("force", false, "覆盖目标路径上已有的文件")
	_ = fs.Parse(args)
	if fs.NArg() < 2 {
		usage()
		os.Exit(2)
	}
	src := volPath(fs.Arg(0))
	dst := volPath(fs.Arg(1))
	// dst 以 / 结尾时按目录理解，自动补上源文件名
	if strings.HasSuffix(fs.Arg(1), "/") || strings.HasSuffix(fs.Arg(1), `\`) {
		dst = path.Join(dst, path.Base(src))
	}
	if src == dst {
		fatal("源和目标是同一个路径")
	}

	cfg, err := config.Load("config.toml")
	if err != nil {
		fatal(fmt.Sprintf("读取配置: %v", err))
	}
	volume, ok := volumeByName(cfg, *name)
	if !ok {
		fatal("卷不存在: " + *name)
	}
	apiHash := cfg.APIHash()
	if apiHash == "" {
		fatal(i18n.T("err.missing_api_hash"))
	}
	key, err := crypto.LoadOrCreateKey("secrets/session.key")
	if err != nil {
		fatal(fmt.Sprintf("%s: %v", i18n.T("err.session_key_prefix"), err))
	}
	idx, err := index.Open(datadir.Path("index-" + *name + ".db"))
	if err != nil {
		fatal(fmt.Sprintf("打开索引: %v", err))
	}
	defer idx.Close()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	source, found, err := idx.Get(ctx, src)
	if err != nil {
		fatal(fmt.Sprintf("读取索引: %v", err))
	}
	if !found {
		fatal("源文件不在索引里，先运行 mytgfs sync: " + src)
	}
	old, hadOld, err := idx.Get(ctx, dst)
	if err != nil {
		fatal(fmt.Sprintf("读取索引: %v", err))
	}
	if hadOld && !*force {
		fatal("目标已存在，加 --force 覆盖: " + dst)
	}

	client := tgclient.New(cfg.Telegram.APIID, apiHash,
		&sessionstore.Encrypted{Path: "secrets/bot.session", Key: key},
		tgclient.Options{})
	err = client.Run(ctx, func(ctx context.Context) error {
		store := telegramstore.New(client.API(), volume.ChatID)
		part, cerr := store.Copy(ctx, &telegramstore.Part{
			MessageID:     source.MessageID,
			DocumentID:    source.DocumentID,
			AccessHash:    source.AccessHash,
			FileReference: source.FileReference,
			Size:          source.Size,
		}, path.Base(dst), "application/octet-stream", tgsync.EncodeCaptionHash(dst, source.Sha256))
		if cerr != nil {
			return cerr
		}
		if err := idx.Put(ctx, index.File{
			Path:          dst,
			MessageID:     part.MessageID,
			DocumentID:    part.DocumentID,
			AccessHash:    part.AccessHash,
			FileReference: part.FileReference,
			Size:          part.Size,
			Mtime:         source.Mtime,
			Sha256:        source.Sha256,
		}); err != nil {
			return err
		}
		if hadOld && old.MessageID > 0 && old.MessageID != part.MessageID {
			if err := store.Delete(ctx, old.MessageID); err != nil {
				fmt.Fprintf(os.Stderr, "删除目标旧消息 %d 失败: %v\n", old.MessageID, err)
			}
		}
		fmt.Printf("COPY %s -> %s doc=%d msg=%d size=%d\n",
			src, dst, part.DocumentID, part.MessageID, part.Size)
		return nil
	})
	if err != nil {
		failErr("复制失败", err)
	}
}

// volPath 把命令行路径规范成卷内绝对路径。
//
// 输入：用户写的路径（可带 Windows 分隔符）
// 输出：以 / 开头的路径
// 预期：反斜杠统一成正斜杠；相对写法补上前导 /
func volPath(p string) string {
	p = strings.ReplaceAll(p, `\`, "/")
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return path.Clean(p)
}

// volumeByName 按卷名取频道 ID。
//
// 输入：cfg 配置；name 卷名
// 输出：卷信息、是否存在
// 预期：没有 volumes 配置时把默认频道当作名为 mytgfs 的卷
func volumeByName(cfg config.Config, name string) (config.Volume, bool) {
	if len(cfg.Volumes) == 0 {
		if name != "mytgfs" {
			return config.Volume{}, false
		}
		return config.Volume{Name: "mytgfs", ChatID: cfg.Telegram.ChannelID}, true
	}
	for _, v := range cfg.Volumes {
		if v.Name == name {
			return v, true
		}
	}
	return config.Volume{}, false
}
