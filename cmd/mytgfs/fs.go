package main

// 文件操作子命令：ls / mkdir / rm / mv / get / search。
//
// 与面板共用 internal/fm + internal/vfs，语义完全一致：
//   - 路径是卷内绝对路径，可省略前导 /，反斜杠自动转换
//   - rm 删目录会递归删除
//   - 所有命令支持 --name 选卷、--json 结构化输出

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path"
	"strings"
	"time"

	"mytgfs/internal/app"
	"mytgfs/internal/datadir"
	"mytgfs/internal/fm"
	"mytgfs/internal/i18n"
	"mytgfs/internal/logging"
	"mytgfs/internal/ui"
	"mytgfs/internal/vfs"
)

// withVolume 打开指定卷并执行 fn，按需启动持久化队列。
//
// 输入：name 卷名（空则第一个卷）；attachQueue 是否启动上传/删改队列；
// fn 具体操作（拿到 app、会话、文件系统与门面）
// 输出：操作失败时进程退出 1
// 预期：索引为空时先用 user 会话按频道历史重建；fn 返回后队列与连接一起关闭
func withVolume(name string, attachQueue bool, fn func(ctx context.Context, a *app.App, vol string, s *app.Session, fsys *vfs.FileSystem, f fm.Files) error) {
	a, err := app.Load(app.Options{})
	if err != nil {
		fatal(fmt.Sprintf("%s: %v", i18n.T("err.config_load"), err))
	}
	_, volName, ok := a.Volume(name)
	if !ok {
		fatalCode("volume_not_found", i18n.T("err.volume_not_found", volName))
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	err = a.Run(ctx, volName, func(ctx context.Context, s *app.Session) error {
		// 索引是缓存：空了就重建，否则 ls/search 会给出空白结果
		if rerr := a.RebuildIndexIfEmpty(ctx, s.Index, s.Volume.ChatID); rerr != nil {
			logging.Warn("err.fs_rebuild", rerr)
		}
		davfs := vfs.New(ctx, s.Index, s.Store(), a.Config().Mount.ReadChunk(), nil)
		davfs.SetVolume(volName)
		davfs.SetTempDir(datadir.Path("tmp"))
		if attachQueue {
			if _, qerr := davfs.AttachQueue(ctx); qerr != nil {
				return qerr
			}
			defer davfs.Close()
		}
		return fn(ctx, a, volName, s, davfs, fm.New(davfs))
	})
	if err != nil {
		failErr(i18n.T("err.fs_prefix"), err)
	}
}

// withFiles 打开只读文件管理接口并执行 fn。
//
// 输入：name 卷名；fn 具体操作
// 输出：操作失败时进程退出 1
// 预期：不启动队列；需要上传/删改的命令用 withVolume(name, true, ...)。
// 声明成变量是为了让测试整体替换掉「加载配置 + 连 Telegram」这一步：
// 生产路径始终指向真实实现，命令体本身（参数解析、输出、错误传播）可被单测覆盖。
var withFiles = func(name string, fn func(ctx context.Context, vol string, f fm.Files) error) {
	withVolume(name, false, func(ctx context.Context, _ *app.App, vol string, _ *app.Session, _ *vfs.FileSystem, f fm.Files) error {
		return fn(ctx, vol, f)
	})
}

// lsEntry 是 ls --json 的单条记录。
//
// 输入：无（由 fm.Entry 拼装）
// 输出：JSON 字段 name/path/dir/size/mod
// 预期：path 是卷内绝对路径，脚本可直接回传给 get/rm
type lsEntry struct {
	Name string    `json:"name"`
	Path string    `json:"path"`
	Dir  bool      `json:"dir"`
	Size int64     `json:"size"`
	Mod  time.Time `json:"mod"`
}

// lsEntries 把 fm.Entry 列表转成带完整路径的 JSON 记录。
//
// 输入：dir 列出的目录；entries 目录项
// 输出：等长切片，Path = dir + name
// 预期：dir 已由 volPath 规范化，path.Join 不会产生重复斜杠
func lsEntries(dir string, entries []fm.Entry) []lsEntry {
	view := make([]lsEntry, 0, len(entries))
	for _, e := range entries {
		view = append(view, lsEntry{
			Name: e.Name,
			Path: path.Join(dir, e.Name),
			Dir:  e.Dir,
			Size: e.Size,
			Mod:  e.Mod,
		})
	}
	return view
}

// printLs 输出目录的人读列表。
//
// 输入：dir 目录路径；entries 目录项
// 输出：每行「类型 大小 修改时间 完整路径」
// 预期：--json 模式下自动静默（ui.Printf 自身判断）
func printLs(dir string, entries []fm.Entry) {
	for _, e := range entries {
		kind := "f"
		if e.Dir {
			kind = "d"
		}
		ui.Printf("%s %12d %s %s\n", kind, e.Size, e.Mod.Format("2006-01-02 15:04"), path.Join(dir, e.Name))
	}
}

// runLs 列出目录内容。
//
// 输入：--name 卷名；可选位置参数目录（默认 --dir / MYTGFS_DIR，再退到 /）
// 输出：每行「类型 大小 修改时间 路径」；--json 输出 entries 数组（含 path）
// 预期：目录不存在时报错退出 1
func runLs(args []string) {
	fset := flag.NewFlagSet("ls", flag.ExitOnError)
	name := fset.String("name", "", "volume name (default: first volume)")
	_ = fset.Parse(args)

	dir := baseDir()
	if fset.NArg() > 0 {
		dir = volPath(fset.Arg(0))
	}
	withFiles(*name, func(ctx context.Context, vol string, f fm.Files) error {
		entries, err := f.List(ctx, dir)
		if err != nil {
			return err
		}
		view := lsEntries(dir, entries)
		ui.Print(map[string]any{"volume": vol, "dir": dir, "entries": view})
		printLs(dir, entries)
		return nil
	})
}

// runMkdir 创建目录。
//
// 输入：--name 卷名；位置参数目录路径
// 输出：MKDIR 行
// 预期：父目录不存在时按上游报错；目录已存在视为成功（与面板一致）
func runMkdir(args []string) {
	fset := flag.NewFlagSet("mkdir", flag.ExitOnError)
	name := fset.String("name", "", "volume name (default: first volume)")
	_ = fset.Parse(args)
	if fset.NArg() < 1 {
		usage()
		os.Exit(2)
	}
	target := volPath(fset.Arg(0))
	withFiles(*name, func(ctx context.Context, vol string, f fm.Files) error {
		if err := f.Mkdir(ctx, target); err != nil {
			return err
		}
		ui.Print(map[string]any{"volume": vol, "mkdir": target})
		ui.Printf("MKDIR %s:%s\n", vol, target)
		return nil
	})
}

// runRm 删除文件或目录。
//
// 输入：--name 卷名；一个或多个路径
// 输出：每个路径一行 RM
// 预期：目录递归删除；任一失败即退出 1，前面的删除不回滚
func runRm(args []string) {
	fset := flag.NewFlagSet("rm", flag.ExitOnError)
	name := fset.String("name", "", "volume name (default: first volume)")
	_ = fset.Parse(args)
	if fset.NArg() < 1 {
		usage()
		os.Exit(2)
	}
	targets := make([]string, 0, fset.NArg())
	for _, a := range fset.Args() {
		targets = append(targets, volPath(a))
	}
	withFiles(*name, func(ctx context.Context, vol string, f fm.Files) error {
		removed, err := removeTargets(ctx, f, targets)
		for _, target := range removed {
			ui.Printf("RM %s:%s\n", vol, target)
		}
		if err != nil {
			return err
		}
		ui.Print(map[string]any{"volume": vol, "removed": removed})
		return nil
	})
}

// removeTargets 依次删除远端路径，返回已成功删除的部分。
//
// 输入：ctx；f 文件接口；targets 卷内路径
// 输出：成功删除的路径（保持输入顺序）；首个失败的错误
// 预期：遇错立刻停止，已删项留在返回列表里，调用方可如实上报
func removeTargets(ctx context.Context, f fm.Files, targets []string) ([]string, error) {
	removed := make([]string, 0, len(targets))
	for _, target := range targets {
		if err := f.Remove(ctx, target); err != nil {
			return removed, err
		}
		removed = append(removed, target)
	}
	return removed, nil
}

// moveTarget 解析 mv 的目标路径。
//
// 输入：arg 用户输入的目标参数；from 已规范化的源路径
// 输出：卷内目标路径
// 预期：尾随 / 或 \ 时按目录处理，补上源文件名；只做词法拼接，不访问远端
func moveTarget(arg, from string) string {
	to := volPath(arg)
	if strings.HasSuffix(arg, "/") || strings.HasSuffix(arg, `\`) {
		return path.Join(to, path.Base(from))
	}
	return to
}

// runMv 移动或改名。
//
// 输入：--name 卷名；源路径、目标路径
// 输出：MV 行
// 预期：目标已存在时按上游报错；跨卷请用 cp
func runMv(args []string) {
	fset := flag.NewFlagSet("mv", flag.ExitOnError)
	name := fset.String("name", "", "volume name (default: first volume)")
	_ = fset.Parse(args)
	if fset.NArg() < 2 {
		usage()
		os.Exit(2)
	}
	from := volPath(fset.Arg(0))
	to := moveTarget(fset.Arg(1), from)
	withFiles(*name, func(ctx context.Context, vol string, f fm.Files) error {
		if err := f.Move(ctx, from, to); err != nil {
			return err
		}
		ui.Print(map[string]any{"volume": vol, "from": from, "to": to})
		ui.Printf("MV %s:%s -> %s\n", vol, from, to)
		return nil
	})
}

// runGet 下载文件到本地。
//
// 输入：--name 卷名；远端路径；可选本地路径（默认当前目录同名，"-" 输出到 stdout）
// 输出：GET 行（含实际写入字节数与续传起点）；文件模式在 stderr 显示进度
// 预期：
//   - 本地已存在且小于远端时从断点继续，不重下已完成的字节
//   - 本地更大或不需要续传时整份重下
//   - --json 或 "-" 直出时不打印进度，stdout 保持纯净
func runGet(args []string) {
	fset := flag.NewFlagSet("get", flag.ExitOnError)
	name := fset.String("name", "", "volume name (default: first volume)")
	_ = fset.Parse(args)
	if fset.NArg() < 1 {
		usage()
		os.Exit(2)
	}
	remote := volPath(fset.Arg(0))
	local := path.Base(remote)
	if fset.NArg() > 1 {
		local = fset.Arg(1)
	}
	// --json 下 stdout 只能是 JSON，二进制内容必须写到文件
	if local == "-" && ui.JSONMode() {
		ui.Error("usage", fmt.Errorf("%s", i18n.T("err.get_stdout_json")))
		os.Exit(2)
	}
	withFiles(*name, func(ctx context.Context, vol string, f fm.Files) error {
		written, resumed, err := getFile(ctx, f, remote, local)
		if err != nil {
			return err
		}
		ui.Print(map[string]any{
			"volume": vol, "path": remote, "local": local,
			"bytes": written, "resumed": resumed,
		})
		ui.Printf("GET %s:%s -> %s bytes=%d resumed=%d\n", vol, remote, local, written, resumed)
		return nil
	})
}

// getFile 把远端文件写到本地或标准输出。
//
// 输入：ctx；f 文件接口；remote 远端路径；local 本地路径或 "-"
// 输出：实际写入总字节数、续传起点字节数、error
// 预期：
//   - 本地是普通文件且比远端短时从该长度续写，否则整份重写
//   - local 为 "-" 时写标准输出且不输出进度
//   - 失败时保留已写入的部分，下次调用可继续
func getFile(ctx context.Context, f fm.Files, remote, local string) (written, resumed int64, err error) {
	info, err := f.Info(ctx, remote)
	if err != nil {
		return 0, 0, err
	}
	if info.Dir {
		return 0, 0, errors.New(i18n.T("err.get_is_dir"))
	}
	total := info.Size
	var dst io.Writer = os.Stdout
	if local != "-" {
		flags := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
		// 只有「本地是普通文件、非空、且比远端短」才续传
		if st, serr := os.Stat(local); serr == nil && st.Mode().IsRegular() && st.Size() > 0 && st.Size() < total {
			flags = os.O_WRONLY | os.O_CREATE
			resumed = st.Size()
		}
		out, oerr := os.OpenFile(local, flags, 0o644)
		if oerr != nil {
			return 0, 0, oerr
		}
		defer out.Close()
		if resumed > 0 {
			if _, serr := out.Seek(resumed, io.SeekStart); serr != nil {
				return 0, 0, serr
			}
		}
		dst = out
	}
	rf, err := f.Open(ctx, remote)
	if err != nil {
		return 0, 0, err
	}
	defer rf.Close()
	if resumed > 0 {
		if _, serr := rf.Seek(resumed, io.SeekStart); serr != nil {
			return 0, 0, serr
		}
	}
	pw := newProgressWriter(dst, os.Stderr, total, resumed, local != "-" && !ui.JSONMode())
	n, err := io.Copy(pw, rf)
	if err != nil {
		return resumed + n, resumed, err
	}
	pw.render(true)
	return resumed + n, resumed, nil
}

// printSearch 输出搜索命中的人读列表。
//
// 输入：hits 命中项（Path 已是卷内绝对路径）
// 输出：每行「类型 大小 路径」
// 预期：--json 模式下自动静默
func printSearch(hits []fm.SearchHit) {
	for _, h := range hits {
		kind := "f"
		if h.Dir {
			kind = "d"
		}
		ui.Printf("%s %12d %s\n", kind, h.Size, h.Path)
	}
}

// runSearch 按文件名递归搜索。
//
// 输入：--name 卷名；--limit 上限（默认 100）；查询词；起始目录取 --dir / MYTGFS_DIR
// 输出：每行「类型 大小 路径」；--json 输出 hits 数组
// 预期：只匹配文件名子串，不读 Telegram，直接查本地索引
func runSearch(args []string) {
	fset := flag.NewFlagSet("search", flag.ExitOnError)
	name := fset.String("name", "", "volume name (default: first volume)")
	limit := fset.Int("limit", 100, "max hits")
	_ = fset.Parse(args)
	if fset.NArg() < 1 {
		usage()
		os.Exit(2)
	}
	query := fset.Arg(0)
	start := baseDir()
	withFiles(*name, func(ctx context.Context, vol string, f fm.Files) error {
		hits, err := f.Search(ctx, start, query, *limit)
		if err != nil {
			return err
		}
		ui.Print(map[string]any{"volume": vol, "dir": start, "query": query, "hits": hits})
		printSearch(hits)
		return nil
	})
}
