package main

// `mytgfs cp` 子命令：频道内直接复制文件。
//
// 复制不下载也不重新上传字节：发一条引用同一 document 的新消息，
// 只换 caption 里的路径。源文件不动，目标路径的旧消息（如存在）会被删掉。
//
// 用法：
//   mytgfs cp [--name mytgfs] [--to <卷名>] [--force] <src> <dst>
// 输入：src 源路径（卷内绝对路径，或以 / 开头的相对写法均可）
// 输出：COPY / SKIP 行；失败退出 1
// 预期：
//   - src 必须在索引里
//   - dst 已存在且内容相同 => 直接跳过，不发消息
//   - dst 已存在且内容不同 => 默认拒绝，--force 才覆盖
//   - --force 表示强制覆盖，不参与跳过

import (
	"cmp"
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
	name := fs.String("name", "", "volume name（空则用第一个卷）")
	to := fs.String("to", "", "目标卷名，留空表示同一卷")
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
	cfg, err := config.Load("config.toml")
	if err != nil {
		fatal(fmt.Sprintf("%s: %v", i18n.T("err.config_load"), err))
	}
	volName := resolveVolumeName(cfg, *name)
	volume, ok := volumeByName(cfg, volName)
	if !ok {
		fatal(i18n.T("err.volume_not_found", volName))
	}
	dstName := cmp.Or(*to, volName)
	dstVolume, ok := volumeByName(cfg, dstName)
	if !ok {
		fatal(i18n.T("err.volume_target_not_found", dstName))
	}
	// 同卷同路径才是无意义操作；跨卷复制到相同路径是合法的
	if src == dst && dstName == volName {
		fatal(i18n.T("err.cp_same_path"))
	}
	apiHash := cfg.APIHash()
	if apiHash == "" {
		fatal(i18n.T("err.missing_api_hash"))
	}
	key, err := crypto.LoadOrCreateKey("secrets/session.key")
	if err != nil {
		fatal(fmt.Sprintf("%s: %v", i18n.T("err.session_key_prefix"), err))
	}
	idx, err := index.Open(datadir.Path("index-" + volName + ".db"))
	if err != nil {
		fatal(fmt.Sprintf(i18n.T("err.index_open"), err))
	}
	defer idx.Close()

	// 目标卷与源卷不同才需要另开索引；同卷复用同一句柄
	dstIdx := idx
	if dstName != volName {
		dstIdx, err = index.Open(datadir.Path("index-" + dstName + ".db"))
		if err != nil {
			fatal(fmt.Sprintf(i18n.T("err.index_open_target"), err))
		}
		defer dstIdx.Close()
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	source, found, err := idx.Get(ctx, src)
	if err != nil {
		fatal(fmt.Sprintf(i18n.T("err.index_read"), err))
	}
	if !found {
		fatal(i18n.T("err.cp_src_missing", src))
	}
	old, hadOld, err := dstIdx.Get(ctx, dst)
	if err != nil {
		fatal(fmt.Sprintf(i18n.T("err.index_read"), err))
	}
	if hadOld && !*force {
		// 目标已有同内容的文件：不发消息，直接跳过
		if sameContent(old, source) {
			fmt.Printf("SKIP %s:%s -> %s:%s doc=%d size=%d\n",
				volName, src, dstName, dst, old.DocumentID, old.Size)
			return
		}
		fatal(i18n.T("err.cp_dst_exists", dst))
	}

	client := tgclient.New(cfg.Telegram.APIID, apiHash,
		&sessionstore.Encrypted{Path: "secrets/bot.session", Key: key},
		tgclient.Options{})
	err = client.Run(ctx, func(ctx context.Context) error {
		srcStore := telegramstore.New(client.API(), volume.ChatID)
		dstStore := srcStore
		if dstVolume.ChatID != volume.ChatID {
			dstStore = telegramstore.New(client.API(), dstVolume.ChatID)
		}
		srcPart := &telegramstore.Part{
			MessageID:     source.MessageID,
			DocumentID:    source.DocumentID,
			AccessHash:    source.AccessHash,
			FileReference: source.FileReference,
			Size:          source.Size,
		}
		caption := tgsync.EncodeCaptionHash(dst, source.Sha256)
		part, refreshed, cerr := dstStore.CopyFrom(ctx, srcStore, srcPart,
			path.Base(dst), "application/octet-stream", caption)
		if cerr != nil {
			return cerr
		}
		// 源 file_reference 被刷新时写回源索引，否则下次还要再刷一次
		if refreshed != nil && dstIdx != idx {
			if err := idx.Replace(ctx, index.File{
				Path:          src,
				MessageID:     refreshed.MessageID,
				DocumentID:    refreshed.DocumentID,
				AccessHash:    refreshed.AccessHash,
				FileReference: refreshed.FileReference,
				Size:          refreshed.Size,
				Mtime:         source.Mtime,
				Sha256:        source.Sha256,
			}); err != nil {
				fmt.Fprintln(os.Stderr, i18n.T("err.index_read", err))
			}
		}
		if err := dstIdx.Put(ctx, index.File{
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
			if err := dstStore.Delete(ctx, old.MessageID); err != nil {
				fmt.Fprintln(os.Stderr, i18n.T("err.cp_delete_old", old.MessageID, err))
			}
		}
		fmt.Printf("COPY %s:%s -> %s:%s doc=%d msg=%d size=%d\n",
			volName, src, dstName, dst, part.DocumentID, part.MessageID, part.Size)
		return nil
	})
	if err != nil {
		failErr(i18n.T("err.cp_prefix"), err)
	}
}

// sameContent 判断两条索引记录是否指向同一份内容。
//
// 输入：dst 目标路径已有记录；src 源记录
// 输出：true 表示大小一致且 sha256 非空并相同
// 预期：重建索引后的旧记录 sha256 可能为空，无法证明相同，一律按不同处理
func sameContent(dst, src index.File) bool {
	return src.Sha256 != "" && dst.Sha256 == src.Sha256 && dst.Size == src.Size
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
