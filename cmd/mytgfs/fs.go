package main

// 文件操作子命令：ls / mkdir / rm / mv / get / search。
//
// 与面板共用 internal/fm + internal/dav，语义完全一致：
//   - 路径是卷内绝对路径，可省略前导 /，反斜杠自动转换
//   - rm 删目录会递归删除
//   - 所有命令支持 --name 选卷、--json 结构化输出

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path"
	"strings"

	"mytgfs/internal/app"
	"mytgfs/internal/datadir"
	"mytgfs/internal/dav"
	"mytgfs/internal/fm"
	"mytgfs/internal/i18n"
	"mytgfs/internal/logging"
	"mytgfs/internal/ui"
)

// withFiles 打开指定卷的文件管理接口并执行 fn。
//
// 输入：name 卷名（空则第一个卷）；fn 具体操作
// 输出：操作失败时进程退出 1
// 预期：索引为空时先用 user 会话按频道历史重建；连接在 fn 返回后关闭
func withFiles(name string, fn func(ctx context.Context, vol string, f fm.Files) error) {
	a, err := app.Load(app.Options{})
	if err != nil {
		fatal(fmt.Sprintf("%s: %v", i18n.T("err.config_load"), err))
	}
	_, volName, ok := a.Volume(name)
	if !ok {
		fatal(i18n.T("err.volume_not_found", volName))
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	err = a.Run(ctx, volName, func(ctx context.Context, s *app.Session) error {
		// 索引是缓存：空了就重建，否则 ls/search 会给出空白结果
		if rerr := a.RebuildIndexIfEmpty(ctx, s.Index, s.Volume.ChatID); rerr != nil {
			logging.Warn("err.fs_rebuild", rerr)
		}
		davfs := dav.New(ctx, s.Index, s.Store(), a.Config().Mount.ReadChunk())
		davfs.SetTempDir(datadir.Path("tmp"))
		return fn(ctx, volName, fm.New(davfs))
	})
	if err != nil {
		failErr(i18n.T("err.fs_prefix"), err)
	}
}

// runLs 列出目录内容。
//
// 输入：--name 卷名；可选位置参数目录（默认 /）
// 输出：每行「类型 大小 修改时间 路径」；--json 输出 entries 数组
// 预期：目录不存在时报错退出 1
func runLs(args []string) {
	fset := flag.NewFlagSet("ls", flag.ExitOnError)
	name := fset.String("name", "", "volume name (default: first volume)")
	_ = fset.Parse(args)

	dir := "/"
	if fset.NArg() > 0 {
		dir = volPath(fset.Arg(0))
	}
	withFiles(*name, func(ctx context.Context, vol string, f fm.Files) error {
		entries, err := f.List(ctx, dir)
		if err != nil {
			return err
		}
		ui.Print(map[string]any{"volume": vol, "dir": dir, "entries": entries})
		for _, e := range entries {
			kind := "f"
			if e.Dir {
				kind = "d"
			}
			ui.Printf("%s %12d %s %s\n", kind, e.Size, e.Mod.Format("2006-01-02 15:04"), path.Join(dir, e.Name))
		}
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
		for _, target := range targets {
			if err := f.Remove(ctx, target); err != nil {
				return err
			}
			ui.Printf("RM %s:%s\n", vol, target)
		}
		ui.Print(map[string]any{"volume": vol, "removed": targets})
		return nil
	})
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
	to := volPath(fset.Arg(1))
	// 目标以 / 结尾时按目录理解，补上源文件名
	if strings.HasSuffix(fset.Arg(1), "/") || strings.HasSuffix(fset.Arg(1), `\`) {
		to = path.Join(to, path.Base(from))
	}
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
// 输出：GET 行（含字节数）
// 预期：本地写入失败即退出 1；已存在的本地文件会被覆盖
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
	withFiles(*name, func(ctx context.Context, vol string, f fm.Files) error {
		rf, err := f.Open(ctx, remote)
		if err != nil {
			return err
		}
		defer rf.Close()
		var dst io.Writer = os.Stdout
		var out *os.File
		if local != "-" {
			out, err = os.Create(local)
			if err != nil {
				return err
			}
			defer out.Close()
			dst = out
		}
		n, err := io.Copy(dst, rf)
		if err != nil {
			return err
		}
		ui.Print(map[string]any{"volume": vol, "path": remote, "local": local, "bytes": n})
		ui.Printf("GET %s:%s -> %s bytes=%d\n", vol, remote, local, n)
		return nil
	})
}

// runSearch 按文件名递归搜索。
//
// 输入：--name 卷名；--dir 起始目录（默认 /）；--limit 上限（默认 100）；查询词
// 输出：每行「类型 大小 路径」；--json 输出 hits 数组
// 预期：只匹配文件名子串，不读 Telegram，直接查本地索引
func runSearch(args []string) {
	fset := flag.NewFlagSet("search", flag.ExitOnError)
	name := fset.String("name", "", "volume name (default: first volume)")
	dir := fset.String("dir", "/", "start directory")
	limit := fset.Int("limit", 100, "max hits")
	_ = fset.Parse(args)
	if fset.NArg() < 1 {
		usage()
		os.Exit(2)
	}
	query := fset.Arg(0)
	start := volPath(*dir)
	withFiles(*name, func(ctx context.Context, vol string, f fm.Files) error {
		hits, err := f.Search(ctx, start, query, *limit)
		if err != nil {
			return err
		}
		ui.Print(map[string]any{"volume": vol, "dir": start, "query": query, "hits": hits})
		for _, h := range hits {
			kind := "f"
			if h.Dir {
				kind = "d"
			}
			ui.Printf("%s %12d %s\n", kind, h.Size, path.Join(start, h.Path))
		}
		return nil
	})
}
