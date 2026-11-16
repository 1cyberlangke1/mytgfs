package main

// `mytgfs shell` 子命令：常驻一个卷会话的交互式命令行。
//
// 用法：
//   mytgfs [--dir 目录] shell [--name 卷]
// 输入：标准输入逐行命令；双引号可包裹含空格的路径
// 输出：命令结果写 stdout，提示符与错误写 stderr
// 预期：
//   - 一次连接、一次索引；cd 之后相对路径按当前目录解析
//   - 单条命令失败只打印错误，不退出；quit/exit 或输入结束才返回
//   - --json 不可用：交互场景的 stdout 要留给命令结果

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"strconv"
	"strings"
	"unicode"

	"mytgfs/internal/app"
	"mytgfs/internal/fm"
	"mytgfs/internal/i18n"
	tgsync "mytgfs/internal/sync"
	"mytgfs/internal/ui"
	"mytgfs/internal/vfs"
)

// errShellQuit 表示用户输入了退出命令。
var errShellQuit = errors.New("quit")

// shell 是一次交互式会话的状态。
type shell struct {
	ctx  context.Context
	a    *app.App
	vol  string
	s    *app.Session
	fsys *vfs.FileSystem
	f    fm.Files
	cwd  string
	out  io.Writer
	errw io.Writer
	// reporter 在文件模式显示上传进度
	reporter *uploadReporter
}

// runShell 启动交互式 shell。
//
// 输入：--name 卷名；位置参数不允许
// 输出：退出码 0；会话建立失败按普通命令失败退出 1
// 预期：--json 直接按用法错误退出 2
func runShell(args []string) {
	if ui.JSONMode() {
		ui.Error("usage", fmt.Errorf("%s", i18n.T("err.shell_json")))
		os.Exit(2)
	}
	fs := flag.NewFlagSet("shell", flag.ExitOnError)
	name := fs.String("name", "", i18n.T("cli.flag.volume"))
	_ = fs.Parse(args)
	if fs.NArg() > 0 {
		usage()
		os.Exit(2)
	}
	withVolume(*name, true, func(ctx context.Context, a *app.App, volName string, s *app.Session, fsys *vfs.FileSystem, f fm.Files) error {
		sh := &shell{
			ctx: ctx, a: a, vol: volName, s: s, fsys: fsys, f: f,
			cwd: baseDir(), out: os.Stdout, errw: os.Stderr,
			reporter: newUploadReporter(os.Stderr),
		}
		return sh.loop(os.Stdin)
	})
}

// loop 逐行读取并执行命令。
//
// 输入：in 命令来源（通常是标准输入）
// 输出：输入结束或 quit 返回 nil；读错误原样返回
// 预期：空行与解析失败都不退出
func (sh *shell) loop(in io.Reader) error {
	interactive := isCharDevice(os.Stdin)
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	if interactive {
		fmt.Fprintln(sh.errw, i18n.T("shell.banner", sh.vol, sh.cwd))
	}
	for {
		if interactive {
			fmt.Fprintf(sh.errw, i18n.T("shell.prompt"), sh.cwd)
		}
		if !sc.Scan() {
			return sc.Err()
		}
		args := splitArgs(sc.Text())
		if len(args) == 0 {
			continue
		}
		if err := sh.dispatch(args); err != nil {
			if errors.Is(err, errShellQuit) {
				return nil
			}
			fmt.Fprintf(sh.errw, "FAIL: %v\n", err)
		}
	}
}

// dispatch 按命令名分发。
//
// 输入：args 首个元素是命令名
// 输出：命令错误；errShellQuit 表示退出
// 预期：未知命令给出提示但不退出
func (sh *shell) dispatch(args []string) error {
	switch args[0] {
	case "help":
		ui.Out(i18n.T("shell.help"))
		return nil
	case "quit", "exit":
		return errShellQuit
	case "pwd":
		ui.Out(sh.cwd)
		return nil
	case "cd":
		return sh.cd(args[1:])
	case "ls":
		return sh.ls(args[1:])
	case "tree":
		return sh.tree(args[1:])
	case "du":
		return sh.du(args[1:])
	case "cat":
		return sh.cat(args[1:])
	case "get":
		return sh.get(args[1:])
	case "put":
		return sh.put(args[1:])
	case "rm":
		return sh.rm(args[1:])
	case "mkdir":
		return sh.mkdir(args[1:])
	case "mv":
		return sh.mv(args[1:])
	case "search":
		return sh.search(args[1:])
	case "sync":
		return sh.sync(args[1:])
	default:
		return fmt.Errorf("%s", i18n.T("shell.unknown", args[0]))
	}
}

// usageErr 生成用法错误。
//
// 输入：syntax 命令语法片段
// 输出：可直接打印的 error
// 预期：与 CLI 子命令的用法提示风格一致
func usageErr(syntax string) error {
	return fmt.Errorf("%s", i18n.T("shell.usage", syntax))
}

// resolve 把 shell 路径转成卷内绝对路径。
//
// 输入：p 用户输入（可相对当前目录）
// 输出：以 / 开头的干净路径
// 预期：绝对路径不受 cwd 影响；空串返回当前目录；反斜杠归一
func (sh *shell) resolve(p string) string {
	p = strings.ReplaceAll(p, `\`, "/")
	if p == "" {
		return sh.cwd
	}
	if strings.HasPrefix(p, "/") {
		return path.Clean(p)
	}
	return path.Join(sh.cwd, p)
}

// splitArgs 按空白切分，支持双引号包裹含空格的路径。
//
// 输入：line 一行命令
// 输出：参数切片；引号本身不保留
// 预期：未闭合的引号把剩余内容当成一个参数，不报错
func splitArgs(line string) []string {
	var out []string
	var cur strings.Builder
	inQuote, has := false, false
	for _, r := range line {
		switch {
		case r == '"':
			inQuote = !inQuote
			has = true
		case unicode.IsSpace(r) && !inQuote:
			if has {
				out = append(out, cur.String())
				cur.Reset()
				has = false
			}
		default:
			cur.WriteRune(r)
			has = true
		}
	}
	if has {
		out = append(out, cur.String())
	}
	return out
}

// isCharDevice 判断文件是否连着终端。
//
// 输入：f 打开的文件
// 输出：是字符设备返回 true
// 预期：重定向输入时不打印提示符
func isCharDevice(f *os.File) bool {
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

// cd 切换当前目录。
//
// 输入：args 0..1 个目录；无参数回到卷根
// 输出：目录不存在或不是目录时报错
// 预期：只在确认是目录后才更新 cwd
func (sh *shell) cd(args []string) error {
	if len(args) > 1 {
		return usageErr("cd [目录]")
	}
	dir := "/"
	if len(args) == 1 {
		dir = sh.resolve(args[0])
	}
	info, err := sh.f.Info(sh.ctx, dir)
	if err != nil {
		return err
	}
	if !info.Dir {
		return fmt.Errorf("%s", i18n.T("shell.not_dir", dir))
	}
	sh.cwd = dir
	return nil
}

// ls 列出目录。
func (sh *shell) ls(args []string) error {
	if len(args) > 1 {
		return usageErr("ls [目录]")
	}
	dir := sh.cwd
	if len(args) == 1 {
		dir = sh.resolve(args[0])
	}
	entries, err := sh.f.List(sh.ctx, dir)
	if err != nil {
		return err
	}
	printLs(dir, entries)
	return nil
}

// tree 打印目录树。
//
// 输入：可选 --depth N / --depth=N 与一个目录
// 输出：树形文本
// 预期：层数 0 表示不限
func (sh *shell) tree(args []string) error {
	depth := 0
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--depth" {
			if i+1 >= len(args) {
				return usageErr("tree [--depth N] [目录]")
			}
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil {
				return usageErr("tree [--depth N] [目录]")
			}
			depth = n
			continue
		}
		if v, ok := strings.CutPrefix(a, "--depth="); ok {
			n, err := strconv.Atoi(v)
			if err != nil {
				return usageErr("tree [--depth N] [目录]")
			}
			depth = n
			continue
		}
		rest = append(rest, a)
	}
	if len(rest) > 1 {
		return usageErr("tree [--depth N] [目录]")
	}
	dir := sh.cwd
	if len(rest) == 1 {
		dir = sh.resolve(rest[0])
	}
	root, err := walkTree(sh.ctx, sh.f, dir, path.Base(dir), 0, depth)
	if err != nil {
		return err
	}
	printTree(root, "", true)
	return nil
}

// du 汇总目录体积。
func (sh *shell) du(args []string) error {
	if len(args) > 1 {
		return usageErr("du [目录]")
	}
	dir := sh.cwd
	if len(args) == 1 {
		dir = sh.resolve(args[0])
	}
	var entries []duEntry
	if _, _, err := walkDu(sh.ctx, sh.f, dir, &entries); err != nil {
		return err
	}
	printDu(entries)
	return nil
}

// cat 输出文件内容。
func (sh *shell) cat(args []string) error {
	if len(args) < 1 {
		return usageErr("cat <路径...>")
	}
	for _, a := range args {
		if err := catOne(sh.ctx, sh.f, sh.resolve(a)); err != nil {
			return err
		}
	}
	return nil
}

// get 下载文件。
//
// 输入：远端路径；可选本地路径（相对进程当前目录，"-" 写标准输出）
// 输出：GET 行
// 预期：与独立 get 命令同样的续传与进度行为
func (sh *shell) get(args []string) error {
	if len(args) < 1 || len(args) > 2 {
		return usageErr("get <远端> [本地]")
	}
	remote := sh.resolve(args[0])
	local := path.Base(remote)
	if len(args) == 2 {
		local = args[1]
	}
	written, resumed, err := getFile(sh.ctx, sh.f, remote, local)
	if err != nil {
		return err
	}
	ui.Outf("GET %s -> %s bytes=%d resumed=%d\n", remote, local, written, resumed)
	return nil
}

// put 上传本地文件或目录。
//
// 输入：可选 --remote 目录（相对当前目录）与若干本地路径（相对进程当前目录）
// 输出：每个文件的 PUT/FAIL 行与汇总
// 预期：与独立 put 命令共用展开、队列与进度逻辑
func (sh *shell) put(args []string) error {
	remoteBase := sh.cwd
	var locals []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--remote" {
			if i+1 >= len(args) {
				return usageErr("put [--remote 目录] <本地...>")
			}
			i++
			remoteBase = sh.resolve(args[i])
			continue
		}
		if v, ok := strings.CutPrefix(a, "--remote="); ok {
			remoteBase = sh.resolve(v)
			continue
		}
		locals = append(locals, a)
	}
	if len(locals) == 0 {
		return usageErr("put [--remote 目录] <本地...>")
	}
	items, err := collectPutItems(locals, remoteBase)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		return errors.New(i18n.T("err.put_no_files"))
	}
	uploaded, failed := putMany(sh.ctx, sh.fsys, items, sh.reporter)
	ui.Out(i18n.T("put.summary", len(uploaded), len(failed)))
	return nil
}

// rm 删除文件或目录。
func (sh *shell) rm(args []string) error {
	if len(args) < 1 {
		return usageErr("rm <路径...>")
	}
	for _, a := range args {
		p := sh.resolve(a)
		if err := sh.f.Remove(sh.ctx, p); err != nil {
			return err
		}
		ui.Outf("RM %s:%s\n", sh.vol, p)
	}
	return nil
}

// mkdir 创建目录。
func (sh *shell) mkdir(args []string) error {
	if len(args) != 1 {
		return usageErr("mkdir <目录>")
	}
	p := sh.resolve(args[0])
	if err := sh.f.Mkdir(sh.ctx, p); err != nil {
		return err
	}
	ui.Outf("MKDIR %s:%s\n", sh.vol, p)
	return nil
}

// mv 移动或改名。
//
// 输入：源、目标；目标以 / 结尾时当作目录并补上源文件名
// 输出：MV 行
// 预期：与独立 mv 命令相同的覆盖检查
func (sh *shell) mv(args []string) error {
	if len(args) != 2 {
		return usageErr("mv <源> <目标>")
	}
	from := sh.resolve(args[0])
	to := sh.resolve(args[1])
	if strings.HasSuffix(args[1], "/") || strings.HasSuffix(args[1], `\`) {
		to = path.Join(to, path.Base(from))
	}
	if err := sh.f.Move(sh.ctx, from, to); err != nil {
		return err
	}
	ui.Outf("MV %s:%s -> %s\n", sh.vol, from, to)
	return nil
}

// search 按文件名递归搜索当前目录。
func (sh *shell) search(args []string) error {
	limit := 100
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--limit" {
			if i+1 >= len(args) {
				return usageErr("search [--limit N] <关键词>")
			}
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil {
				return usageErr("search [--limit N] <关键词>")
			}
			limit = n
			continue
		}
		if v, ok := strings.CutPrefix(a, "--limit="); ok {
			n, err := strconv.Atoi(v)
			if err != nil {
				return usageErr("search [--limit N] <关键词>")
			}
			limit = n
			continue
		}
		rest = append(rest, a)
	}
	if len(rest) != 1 {
		return usageErr("search [--limit N] <关键词>")
	}
	hits, err := sh.f.Search(sh.ctx, sh.cwd, rest[0], limit)
	if err != nil {
		return err
	}
	printSearch(hits)
	return nil
}

// sync 应用频道增量或重建索引。
//
// 输入：可选 --limit N / --limit=N 与 --rebuild
// 输出：统计行
// 预期：只作用于当前 shell 的卷
func (sh *shell) sync(args []string) error {
	limit := 100
	rebuild := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--rebuild" {
			rebuild = true
			continue
		}
		if a == "--limit" {
			if i+1 >= len(args) {
				return usageErr("sync [--limit N] [--rebuild]")
			}
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil {
				return usageErr("sync [--limit N] [--rebuild]")
			}
			limit = n
			continue
		}
		if v, ok := strings.CutPrefix(a, "--limit="); ok {
			n, err := strconv.Atoi(v)
			if err != nil {
				return usageErr("sync [--limit N] [--rebuild]")
			}
			limit = n
			continue
		}
		return usageErr("sync [--limit N] [--rebuild]")
	}
	if rebuild {
		nodes, err := sh.a.AllNodes(sh.ctx, sh.s.Volume.ChatID)
		if err != nil {
			return err
		}
		n, err := tgsync.Rebuild(sh.ctx, sh.s.Index, nodes)
		if err != nil {
			return err
		}
		ui.Out(i18n.T("cli.rebuild_stats", n))
		return nil
	}
	d := &tgsync.TelegramChannelDiffer{API: sh.s.Client.API(), ChannelID: sh.s.Volume.ChatID}
	res, err := tgsync.SyncChannel(sh.ctx, sh.s.Index, d, sh.s.Store(), limit)
	if err != nil {
		return err
	}
	if res.Truncated {
		ui.Out(i18n.T("warn.diff_too_long"))
	}
	ui.Out(i18n.T("cli.sync_stats", res.Pulled, res.Deleted, res.Pts, res.Rounds))
	return nil
}
