package main

// `mytgfs put` 子命令：批量上传本地文件或目录。
//
// 用法：
//   mytgfs put [--name 卷] [--remote /远程目录] <本地文件或目录...>
// 输入：本地路径（可多个）；目录会递归展开
// 输出：每个文件的 PUT 结果行；最后一行汇总
// 预期：
//   - 所有上传走同一套持久化队列，避免批量时触发 FLOOD_WAIT
//   - 按内容 sha256 续传：中断后再跑同一命令会跳过已传分片
//   - 同内容已在索引里时发引用消息，零字节上传
//   - 索引同时写入 sha256，后续可用 cp 做引用复制

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"mytgfs/internal/app"
	"mytgfs/internal/fm"
	"mytgfs/internal/i18n"
	"mytgfs/internal/index"
	"mytgfs/internal/telegramstore"
	"mytgfs/internal/ui"
	"mytgfs/internal/vfs"
)

// putItem 是一个待上传的本地文件。
type putItem struct {
	local  string
	remote string
}

// putUploaded 是一个已上传文件的结构化结果。
type putUploaded struct {
	Path      string `json:"path"`
	Local     string `json:"local"`
	Size      int64  `json:"size"`
	MessageID int    `json:"message_id"`
	Sha256    string `json:"sha256,omitempty"`
}

// putFailure 是一个上传失败的条目。
type putFailure struct {
	Path  string `json:"path"`
	Local string `json:"local"`
	Error string `json:"error"`
}

// runPut 处理批量上传。
func runPut(args []string) {
	fs := flag.NewFlagSet("put", flag.ExitOnError)
	name := fs.String("name", "", i18n.T("cli.flag.volume"))
	remote := fs.String("remote", "/", i18n.T("cli.flag.remote_base"))
	_ = fs.Parse(args)
	if fs.NArg() == 0 {
		usage()
		os.Exit(2)
	}

	items, err := collectPutItems(fs.Args(), *remote)
	if err != nil {
		fatal(err.Error())
	}
	if len(items) == 0 {
		fatalCode("no_files", i18n.T("err.put_no_files"))
	}

	a, err := app.Load(app.Options{})
	if err != nil {
		fatal(fmt.Sprintf("%s: %v", i18n.T("err.config_load"), err))
	}
	_, volName, ok := a.Volume(*name)
	if !ok {
		fatalCode("volume_not_found", i18n.T("err.volume_not_found", volName))
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	okCount, failCount := 0, 0
	uploaded := make([]putUploaded, 0, len(items))
	failed := make([]putFailure, 0)
	err = a.Run(ctx, volName, func(ctx context.Context, s *app.Session) error {
		fsys := vfs.New(ctx, s.Index, s.Store(), a.Config().Mount.ReadChunk(), nil)
		fsys.SetVolume(volName)
		// 与面板共用同一套持久化队列：中途断网/断电后任务还在
		if _, qerr := fsys.AttachQueue(ctx); qerr != nil {
			return qerr
		}
		defer fsys.Close()

		for _, it := range items {
			node, perr := putOne(ctx, fsys, it)
			if perr != nil {
				failCount++
				failed = append(failed, putFailure{Path: it.remote, Local: it.local, Error: perr.Error()})
				ui.Out(i18n.T("put.fail", it.remote, perr))
				continue
			}
			okCount++
			uploaded = append(uploaded, putUploaded{
				Path:      it.remote,
				Local:     it.local,
				Size:      node.Size,
				MessageID: node.ID,
				Sha256:    node.Sha256,
			})
			ui.Out(i18n.T("put.ok", it.remote, node.Size, node.ID))
		}
		return nil
	})
	if err != nil {
		failErr(i18n.T("err.put_prefix"), err)
	}
	ui.Print(map[string]any{
		"volume":   volName,
		"uploaded": uploaded,
		"failed":   failed,
		"counts":   map[string]int{"uploaded": okCount, "failed": failCount},
	})
	ui.Out(i18n.T("put.summary", okCount, failCount))
	if failCount > 0 {
		os.Exit(1)
	}
}

// putOne 上传单个文件并返回落库后的索引记录。
//
// 输入：ctx；fsys 卷内文件系统（已挂持久化队列）；it 本地与远程路径
// 输出：上传后的节点记录（含消息号、大小、sha256）；失败时为 error
// 预期：目标父目录按需创建；写入走临时文件 + 覆盖语义
func putOne(ctx context.Context, fsys *vfs.FileSystem, it putItem) (index.Node, error) {
	var zero index.Node
	f, err := os.Open(it.local)
	if err != nil {
		return zero, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return zero, err
	}
	if st.Size() > telegramstore.MaxFileSize {
		return zero, telegramstore.ErrTooLarge
	}

	if err := mkdirAll(ctx, fsys, path.Dir(it.remote)); err != nil {
		return zero, err
	}
	if err := fm.New(fsys).Upload(ctx, it.remote, f); err != nil {
		return zero, err
	}
	n, ok, err := fsys.Index().NodeByPath(ctx, it.remote)
	if err != nil {
		return zero, err
	}
	if !ok {
		return zero, os.ErrNotExist
	}
	return n, nil
}

// mkdirAll 按需创建远程目录。
//
// 输入：ctx；fsys 卷内文件系统；dir 目录路径
// 输出：error
// 预期：已存在的层级直接跳过；父目录不存在时按层级顺序创建
func mkdirAll(ctx context.Context, fsys *vfs.FileSystem, dir string) error {
	dir = volPath(dir)
	if dir == "/" {
		return nil
	}
	var cur strings.Builder
	for seg := range strings.SplitSeq(strings.Trim(dir, "/"), "/") {
		if seg == "" {
			continue
		}
		cur.WriteString("/" + seg)
		if err := fsys.Mkdir(ctx, cur.String(), 0o755); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
	}
	return nil
}

// collectPutItems 展开命令行里的文件与目录。
//
// 输入：args 本地路径；remoteBase 远程根目录
// 输出：待上传列表，按远程路径排序（顺序稳定，便于阅读日志）
// 预期：目录递归展开，符号链接不跟随；空目录跳过
func collectPutItems(args []string, remoteBase string) ([]putItem, error) {
	var items []putItem
	base := volPath(remoteBase)
	for _, arg := range args {
		info, err := os.Stat(arg)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			items = append(items, putItem{local: arg, remote: path.Join(base, filepath.Base(arg))})
			continue
		}
		root := filepath.Clean(arg)
		err = filepath.Walk(root, func(p string, fi os.FileInfo, werr error) error {
			if werr != nil {
				return werr
			}
			if fi.IsDir() || !fi.Mode().IsRegular() {
				return nil
			}
			rel, rerr := filepath.Rel(root, p)
			if rerr != nil {
				return rerr
			}
			items = append(items, putItem{
				local:  p,
				remote: path.Join(base, filepath.ToSlash(rel)),
			})
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	slices.SortFunc(items, func(a, b putItem) int { return cmp.Compare(a.remote, b.remote) })
	return items, nil
}
