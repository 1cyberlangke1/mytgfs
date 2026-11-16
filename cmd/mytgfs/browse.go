package main

// 只读浏览子命令：tree / du / cat。
//
// 与 ls 共用 fm.Files：只读本地索引，不向 Telegram 发请求。
//   - tree 递归打印目录树，可选限制层数
//   - du 后序汇总每个目录的文件数与字节数
//   - cat 把文件内容直接写到标准输出，不支持 --json

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path"

	"mytgfs/internal/fm"
	"mytgfs/internal/i18n"
	"mytgfs/internal/ui"
)

// treeNode 是 tree 的一层节点。
//
// 输入：递归列出目录得到
// 输出：JSON 字段 name/path/dir/size/children
// 预期：目录的 size 是子项合计；maxDepth 截断时该层 children 为空
type treeNode struct {
	Name     string      `json:"name"`
	Path     string      `json:"path"`
	Dir      bool        `json:"dir"`
	Size     int64       `json:"size"`
	Children []*treeNode `json:"children,omitempty"`
}

// runTree 递归打印目录树。
//
// 输入：--name 卷名；--depth 最大层数（0 表示不限）；可选目录（默认基准目录）
// 输出：树形文本；--json 输出嵌套节点
// 预期：目录不存在时报错退出 1；目录在前、同类型按名字排序（由 fm.List 保证）
func runTree(args []string) {
	fset := flag.NewFlagSet("tree", flag.ExitOnError)
	name := fset.String("name", "", "volume name (default: first volume)")
	depth := fset.Int("depth", 0, "max depth below the start directory (0 = unlimited)")
	_ = fset.Parse(args)

	dir := baseDir()
	if fset.NArg() > 0 {
		dir = volPath(fset.Arg(0))
	}
	withFiles(*name, func(ctx context.Context, vol string, f fm.Files) error {
		root, err := walkTree(ctx, f, dir, path.Base(dir), 0, *depth)
		if err != nil {
			return err
		}
		ui.Print(map[string]any{"volume": vol, "dir": dir, "tree": root})
		printTree(root, "", true)
		return nil
	})
}

// walkTree 递归构造目录树。
//
// 输入：dir 目录路径；name 展示名；level 当前层；maxDepth 最大层数（<=0 不限）
// 输出：该目录节点，目录 size 为子文件合计
// 预期：level 到达 maxDepth 时只返回目录本身，不再读子目录
func walkTree(ctx context.Context, f fm.Files, dir, name string, level, maxDepth int) (*treeNode, error) {
	n := &treeNode{Name: name, Path: dir, Dir: true}
	if maxDepth > 0 && level >= maxDepth {
		return n, nil
	}
	entries, err := f.List(ctx, dir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		p := path.Join(dir, e.Name)
		if e.Dir {
			c, cerr := walkTree(ctx, f, p, e.Name, level+1, maxDepth)
			if cerr != nil {
				return nil, cerr
			}
			n.Children = append(n.Children, c)
			n.Size += c.Size
			continue
		}
		n.Children = append(n.Children, &treeNode{Name: e.Name, Path: p, Size: e.Size})
		n.Size += e.Size
	}
	return n, nil
}

// printTree 把节点写成人读树形文本。
//
// 输入：n 当前节点；prefix 子层缩进前缀；root 是否为根
// 输出：写到标准输出
// 预期：文件带字节数，目录只显示名字；最后一层用 └── 收尾
func printTree(n *treeNode, prefix string, root bool) {
	if root {
		ui.Printf("%s\n", n.Path)
	}
	for i, c := range n.Children {
		last := i == len(n.Children)-1
		branch, pad := "├── ", "│   "
		if last {
			branch, pad = "└── ", "    "
		}
		if c.Dir {
			ui.Printf("%s%s%s/\n", prefix, branch, c.Name)
		} else {
			ui.Printf("%s%s%s  %d\n", prefix, branch, c.Name, c.Size)
		}
		printTree(c, prefix+pad, false)
	}
}

// duEntry 是 du 输出的一个目录汇总。
//
// 输入：后序遍历得到
// 输出：JSON 字段 path/files/size
// 预期：files 是该目录子树里文件总数，size 是这些文件字节合计
type duEntry struct {
	Path  string `json:"path"`
	Files int    `json:"files"`
	Size  int64  `json:"size"`
}

// runDu 汇总目录占用的文件数与字节数。
//
// 输入：--name 卷名；可选目录（默认基准目录）
// 输出：每个目录一行「字节数 文件数 路径」（子目录在前）；--json 输出 entries 数组
// 预期：只统计文件；目录的数值包含整棵子树
func runDu(args []string) {
	fset := flag.NewFlagSet("du", flag.ExitOnError)
	name := fset.String("name", "", "volume name (default: first volume)")
	_ = fset.Parse(args)

	dir := baseDir()
	if fset.NArg() > 0 {
		dir = volPath(fset.Arg(0))
	}
	withFiles(*name, func(ctx context.Context, vol string, f fm.Files) error {
		var entries []duEntry
		size, files, err := walkDu(ctx, f, dir, &entries)
		if err != nil {
			return err
		}
		ui.Print(map[string]any{
			"volume":  vol,
			"dir":     dir,
			"total":   duEntry{Path: dir, Files: files, Size: size},
			"entries": entries,
		})
		printDu(entries)
		return nil
	})
}

// printDu 输出目录体积的人读列表。
//
// 输入：entries 后序收集的目录汇总
// 输出：每行「字节数 文件数 路径」
// 预期：--json 模式下自动静默
func printDu(entries []duEntry) {
	for _, e := range entries {
		ui.Printf("%12d  %6d  %s\n", e.Size, e.Files, e.Path)
	}
}

// walkDu 后序汇总目录体积。
//
// 输入：dir 目录路径；entries 收集输出顺序（子目录先于父目录）
// 输出：子树字节合计、文件数
// 预期：每个目录都会追加一条记录，根目录在最后
func walkDu(ctx context.Context, f fm.Files, dir string, entries *[]duEntry) (int64, int, error) {
	list, err := f.List(ctx, dir)
	if err != nil {
		return 0, 0, err
	}
	var size int64
	var files int
	for _, e := range list {
		if e.Dir {
			cs, cf, cerr := walkDu(ctx, f, path.Join(dir, e.Name), entries)
			if cerr != nil {
				return 0, 0, cerr
			}
			size += cs
			files += cf
			continue
		}
		size += e.Size
		files++
	}
	*entries = append(*entries, duEntry{Path: dir, Files: files, Size: size})
	return size, files, nil
}

// runCat 把文件内容写到标准输出。
//
// 输入：--name 卷名；一个或多个远端路径
// 输出：原始字节流，多文件按参数顺序直接拼接
// 预期：--json 下拒绝执行（stdout 只能放 JSON），按用法错误退出 2
func runCat(args []string) {
	fset := flag.NewFlagSet("cat", flag.ExitOnError)
	name := fset.String("name", "", "volume name (default: first volume)")
	_ = fset.Parse(args)
	if fset.NArg() < 1 {
		usage()
		os.Exit(2)
	}
	if ui.JSONMode() {
		ui.Error("usage", fmt.Errorf("%s", i18n.T("err.cat_json")))
		os.Exit(2)
	}
	targets := make([]string, 0, fset.NArg())
	for _, a := range fset.Args() {
		targets = append(targets, volPath(a))
	}
	withFiles(*name, func(ctx context.Context, _ string, f fm.Files) error {
		return catAll(ctx, f, targets, os.Stdout)
	})
}

// catAll 按参数顺序把多个远端文件写到 w。
//
// 输入：ctx；f 文件接口；targets 卷内路径；w 输出目标
// 输出：首个失败的错误
// 预期：遇错立即停止；已写出的内容保持原样，不做回滚
func catAll(ctx context.Context, f fm.Files, targets []string, w io.Writer) error {
	for _, p := range targets {
		if err := catOne(ctx, f, p, w); err != nil {
			return err
		}
	}
	return nil
}

// catOne 输出单个文件内容。
//
// 输入：ctx；f 文件接口；p 远端路径；w 输出目标
// 输出：内容写到 w
// 预期：复制或关闭失败都上报；不缓冲整份内容
func catOne(ctx context.Context, f fm.Files, p string, w io.Writer) error {
	rf, err := f.Open(ctx, p)
	if err != nil {
		return err
	}
	_, cerr := io.Copy(w, rf)
	if err := rf.Close(); err != nil && cerr == nil {
		return err
	}
	return cerr
}
