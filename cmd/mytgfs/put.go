package main

// `mytgfs put` 子命令：批量上传本地文件或目录。
//
// 用法：
//   mytgfs put [--name 卷] [--remote /远程目录] <本地文件或目录...>
// 输入：本地路径（可多个）；目录会递归展开
// 输出：每个文件的 PUT 结果行；最后一行汇总
// 预期：
//   - 所有上传走串行队列，避免批量时触发 FLOOD_WAIT
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
	"mytgfs/internal/queue"
	"mytgfs/internal/telegramstore"
	"mytgfs/internal/ui"
	"mytgfs/internal/vfs"
)

// putItem 是一个待上传的本地文件。
type putItem struct {
	local  string
	remote string
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
		fatal(i18n.T("err.put_no_files"))
	}

	a, err := app.Load(app.Options{})
	if err != nil {
		fatal(fmt.Sprintf("%s: %v", i18n.T("err.config_load"), err))
	}
	_, volName, ok := a.Volume(*name)
	if !ok {
		fatal(i18n.T("err.volume_not_found", volName))
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	okCount, failCount := 0, 0
	err = a.Run(ctx, volName, func(ctx context.Context, s *app.Session) error {
		q := queue.New(1, 3)

		for _, it := range items {
			if err := q.Do(ctx, it.remote, func(ctx context.Context) error {
				return putOne(ctx, s.Index, s.Store(), a.Config().Mount.ReadChunk(), it)
			}); err != nil {
				failCount++
				ui.Out(i18n.T("put.fail", it.remote, err))
				continue
			}
			okCount++
		}
		return nil
	})
	if err != nil {
		failErr(i18n.T("err.put_prefix"), err)
	}
	ui.Out(i18n.T("put.summary", okCount, failCount))
	if failCount > 0 {
		os.Exit(1)
	}
}

// putOne 上传单个文件并登记目录项。
//
// 输入：ctx；idx 索引；store 存储；chunk 读块大小；it 本地与远程路径
// 输出：error
// 预期：目标父目录按需创建；写入走 dav 的临时文件 + 覆盖语义
func putOne(ctx context.Context, idx *index.Index, store *telegramstore.Store, chunk int64, it putItem) error {
	f, err := os.Open(it.local)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if st.Size() > telegramstore.MaxFileSize {
		return telegramstore.ErrTooLarge
	}

	fsys := vfs.New(ctx, idx, store, chunk)
	if err := mkdirAll(ctx, fsys, path.Dir(it.remote)); err != nil {
		return err
	}
	if err := fm.New(fsys).Upload(ctx, it.remote, f); err != nil {
		return err
	}
	n, ok, err := idx.NodeByPath(ctx, it.remote)
	if err != nil {
		return err
	}
	if !ok {
		return os.ErrNotExist
	}
	ui.Out(i18n.T("put.ok", it.remote, n.Size, n.ID))
	return nil
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

// remoteDir 规范化远程目录，保证以 / 开头。
func remoteDir(p string) string {
	p = strings.ReplaceAll(p, `\`, "/")
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return path.Clean(p)
}
