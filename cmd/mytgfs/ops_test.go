package main

// 命令层单测：一部分验证纯逻辑（删除保留已处理部分、目标目录补名、拼接顺序），
// 另一部分把 withFiles 换成假实现直接跑 runXxx，覆盖参数解析、调用顺序、
// 人读输出与错误传播，全程不加载配置也不连网。

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mytgfs/internal/fm"
	"mytgfs/internal/ui"
)

// stubFiles 把 withFiles 换成直接执行命令体的假实现。
//
// 输入：t 用于注册恢复；f 假文件接口；vol 命令看到的卷名
// 输出：指向命令体返回错误的指针（命令内部 return 的错误落在 *err）
// 预期：测试结束恢复真实实现；不触碰 config.toml、索引与 Telegram 连接；
// 期间强制人读模式，输出形态不受其他用例留下的 --json 影响
func stubFiles(t *testing.T, f fm.Files, vol string) *error {
	t.Helper()
	prev := withFiles
	wasJSON := ui.JSONMode()
	ui.DisableJSON()
	err := new(error)
	withFiles = func(_ string, fn func(ctx context.Context, vol string, f fm.Files) error) {
		*err = fn(t.Context(), vol, f)
	}
	t.Cleanup(func() {
		withFiles = prev
		if wasJSON {
			ui.EnableJSON()
		}
	})
	return err
}

// captureStdout 把 os.Stdout 临时换成文件，返回 fn 期间写出的内容。
//
// 输入：t 用于临时目录；fn 要测量的输出代码
// 输出：stdout 上的原始字节（JSON 模式会拿到 JSON 行）
// 预期：ui 的写函数在调用时读 os.Stdout，因此替换立即生效；结束后复原
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	return captureFile(t, &os.Stdout, fn)
}

// captureStderr 把 os.Stderr 临时换成文件，返回 fn 期间写出的内容。
//
// 输入：t 用于临时目录；fn 要测量的输出代码
// 输出：stderr 上的原始字节（人读错误、进度行都走这里）
// 预期：与 captureStdout 同一套替换/复原流程，两个流互不干扰
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	return captureFile(t, &os.Stderr, fn)
}

// captureFile 把目标标准流换成临时文件并在结束后复原。
//
// 输入：t 用于临时目录；target 指向 os.Stdout 或 os.Stderr 的变量；fn 被测代码
// 输出：目标流上写出的原始字节
// 预期：先记下原流再替换，defer 里恢复，测试失败也不会污染其他用例
func captureFile(t *testing.T, target **os.File, fn func()) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "stream")
	out, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	prev := *target
	*target = out
	defer func() { *target = prev }()
	fn()
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestRunLs 列出目录：命令体成功，人读模式每行带完整路径。
func TestRunLs(t *testing.T) {
	runErr := stubFiles(t, testTree(), "demo")
	got := captureStdout(t, func() { runLs([]string{"/root"}) })
	if *runErr != nil {
		t.Fatalf("runLs: %v", *runErr)
	}
	if !strings.Contains(got, "/root/a.txt") || !strings.Contains(got, "/root/sub") {
		t.Fatalf("人读输出 = %q", got)
	}
}

// TestRunMkdir 建目录命令把规范化后的路径交给 fm。
func TestRunMkdir(t *testing.T) {
	f := &fakeFiles{}
	runErr := stubFiles(t, f, "demo")
	captureStdout(t, func() { runMkdir([]string{`sub\new`}) })
	if *runErr != nil {
		t.Fatalf("runMkdir: %v", *runErr)
	}
	if len(f.mkdirs) != 1 || f.mkdirs[0] != "/sub/new" {
		t.Fatalf("mkdirs = %v，期望 [/sub/new]", f.mkdirs)
	}
}

// TestRunRm 删除多个路径：中途失败时已删项照常上报，错误向上返回。
func TestRunRm(t *testing.T) {
	boom := errors.New("boom")
	f := &fakeFiles{failOn: map[string]error{"/b.txt": boom}}
	runErr := stubFiles(t, f, "demo")
	captureStdout(t, func() { runRm([]string{"/a.txt", "/b.txt", "/c.txt"}) })
	if !errors.Is(*runErr, boom) {
		t.Fatalf("err = %v，期望 boom", *runErr)
	}
	if len(f.removes) != 1 || f.removes[0] != "/a.txt" {
		t.Fatalf("removes = %v，期望只删 /a.txt", f.removes)
	}
}

// TestRunMv 目标带尾斜杠时补源文件名，否则原样使用。
func TestRunMv(t *testing.T) {
	f := &fakeFiles{}
	runErr := stubFiles(t, f, "demo")
	captureStdout(t, func() { runMv([]string{"/a.txt", "/dest/"}) })
	if *runErr != nil {
		t.Fatalf("runMv: %v", *runErr)
	}
	if len(f.moves) != 1 || f.moves[0] != [2]string{"/a.txt", "/dest/a.txt"} {
		t.Fatalf("moves = %v，期望 /a.txt -> /dest/a.txt", f.moves)
	}
}

// TestRunSearch 搜索命令把查询与上限透传给 fm，并输出命中。
func TestRunSearch(t *testing.T) {
	f := &fakeFiles{searchHits: []fm.SearchHit{{Path: "/root/a.txt", Size: 10}}}
	runErr := stubFiles(t, f, "demo")
	got := captureStdout(t, func() { runSearch([]string{"--limit", "5", "a.txt"}) })
	if *runErr != nil {
		t.Fatalf("runSearch: %v", *runErr)
	}
	if !strings.Contains(got, "/root/a.txt") {
		t.Fatalf("人读输出 = %q", got)
	}
}

// TestRunGet 下载命令把远端内容写到本地文件。
func TestRunGet(t *testing.T) {
	runErr := stubFiles(t, newGetFiles(), "demo")
	local := filepath.Join(t.TempDir(), "a.bin")
	captureStdout(t, func() { runGet([]string{"/a.bin", local}) })
	if *runErr != nil {
		t.Fatalf("runGet: %v", *runErr)
	}
	got, err := os.ReadFile(local)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "0123456789" {
		t.Fatalf("本地内容 = %q", got)
	}
}

// TestRunTree 树形命令递归成功，--depth 为 1 时截断子目录。
func TestRunTree(t *testing.T) {
	runErr := stubFiles(t, testTree(), "demo")
	got := captureStdout(t, func() { runTree([]string{"--depth", "1", "/root"}) })
	if *runErr != nil {
		t.Fatalf("runTree: %v", *runErr)
	}
	if !strings.Contains(got, "/root") || !strings.Contains(got, "a.txt") {
		t.Fatalf("人读输出 = %q", got)
	}
	if strings.Contains(got, "deep") {
		t.Fatalf("--depth 1 不应展开 deep: %q", got)
	}
}

// TestRunDu 目录汇总命令成功，人读模式列出每个目录。
func TestRunDu(t *testing.T) {
	runErr := stubFiles(t, testTree(), "demo")
	got := captureStdout(t, func() { runDu([]string{"/root"}) })
	if *runErr != nil {
		t.Fatalf("runDu: %v", *runErr)
	}
	if !strings.Contains(got, "/root/sub/deep") {
		t.Fatalf("人读输出 = %q", got)
	}
}

// TestRunCat cat 把文件内容直写标准输出，人读模式没有额外文案。
func TestRunCat(t *testing.T) {
	f := &fakeFiles{data: map[string][]byte{"/a.txt": []byte("hello")}}
	runErr := stubFiles(t, f, "demo")
	got := captureStdout(t, func() { runCat([]string{"/a.txt"}) })
	if *runErr != nil {
		t.Fatalf("runCat: %v", *runErr)
	}
	if got != "hello" {
		t.Fatalf("stdout = %q，期望 hello", got)
	}
}

// TestUsage 人读模式下把用法写到 stderr。
func TestUsage(t *testing.T) {
	prev := ui.JSONMode()
	ui.DisableJSON()
	t.Cleanup(func() {
		if prev {
			ui.EnableJSON()
		}
	})
	if got := captureStderr(t, usage); !strings.Contains(got, "mytgfs") {
		t.Fatalf("usage 输出 = %q", got)
	}
}

// TestPrintPutPlan --dry-run 清单按文件输出本地路径、远端路径与字节数。
func TestPrintPutPlan(t *testing.T) {
	local := filepath.Join(t.TempDir(), "a.bin")
	if err := os.WriteFile(local, []byte("12345"), 0o600); err != nil {
		t.Fatal(err)
	}
	ui.DisableJSON()
	got := captureStdout(t, func() {
		printPutPlan("demo", []putItem{{local: local, remote: "/a.bin"}})
	})
	if !strings.Contains(got, local) || !strings.Contains(got, "/a.bin") {
		t.Fatalf("计划输出 = %q", got)
	}
}

// TestRemoveTargets 全成功时按序返回全部路径；中途失败只保留已删项。
func TestRemoveTargets(t *testing.T) {
	t.Run("all", func(t *testing.T) {
		f := &fakeFiles{}
		removed, err := removeTargets(t.Context(), f, []string{"/a", "/b", "/c"})
		if err != nil {
			t.Fatalf("removeTargets: %v", err)
		}
		if len(removed) != 3 || removed[2] != "/c" {
			t.Fatalf("removed = %v", removed)
		}
		if len(f.removes) != 3 {
			t.Fatalf("实际删除 = %v", f.removes)
		}
	})

	t.Run("fail midway", func(t *testing.T) {
		boom := errors.New("boom")
		f := &fakeFiles{failOn: map[string]error{"/b": boom}}
		removed, err := removeTargets(t.Context(), f, []string{"/a", "/b", "/c"})
		if !errors.Is(err, boom) {
			t.Fatalf("err = %v，期望 boom", err)
		}
		if len(removed) != 1 || removed[0] != "/a" {
			t.Fatalf("removed = %v，期望只含 /a", removed)
		}
		if len(f.removes) != 1 {
			t.Fatalf("实际删除 = %v，失败后不应继续", f.removes)
		}
	})

	t.Run("empty", func(t *testing.T) {
		removed, err := removeTargets(t.Context(), &fakeFiles{}, nil)
		if err != nil || len(removed) != 0 {
			t.Fatalf("removed = %v, err = %v", removed, err)
		}
	})
}

// TestMoveTarget 目标以分隔符结尾时补上源文件名，否则按原路径。
func TestMoveTarget(t *testing.T) {
	cases := []struct {
		name string
		arg  string
		from string
		want string
	}{
		{"trailing slash", "/dst/", "/src/x.txt", "/dst/x.txt"},
		{"trailing backslash", `\dst\`, "/src/x.txt", "/dst/x.txt"},
		{"plain path", "/dst/y.txt", "/src/x.txt", "/dst/y.txt"},
		{"no leading slash", "dst/", "/src/x.txt", "/dst/x.txt"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := moveTarget(c.arg, c.from); got != c.want {
				t.Fatalf("moveTarget(%q, %q) = %q，期望 %q", c.arg, c.from, got, c.want)
			}
		})
	}
}

// TestCatAll 按参数顺序拼接内容；缺文件时立即停止且不写后续文件。
func TestCatAll(t *testing.T) {
	t.Run("concat order", func(t *testing.T) {
		f := &fakeFiles{data: map[string][]byte{
			"/a.txt": []byte("alpha"),
			"/b.txt": []byte("-beta"),
		}}
		var buf bytes.Buffer
		if err := catAll(t.Context(), f, []string{"/a.txt", "/b.txt"}, &buf); err != nil {
			t.Fatalf("catAll: %v", err)
		}
		if buf.String() != "alpha-beta" {
			t.Fatalf("输出 = %q", buf.String())
		}
	})

	t.Run("missing stops", func(t *testing.T) {
		f := &fakeFiles{data: map[string][]byte{"/a.txt": []byte("alpha")}}
		var buf bytes.Buffer
		err := catAll(t.Context(), f, []string{"/a.txt", "/gone.txt", "/b.txt"}, &buf)
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("err = %v，期望 os.ErrNotExist", err)
		}
		if buf.String() != "alpha" {
			t.Fatalf("输出 = %q，缺失文件后不应继续", buf.String())
		}
	})

	t.Run("empty list", func(t *testing.T) {
		var buf bytes.Buffer
		if err := catAll(t.Context(), &fakeFiles{}, nil, &buf); err != nil {
			t.Fatalf("catAll: %v", err)
		}
		if buf.Len() != 0 {
			t.Fatalf("输出 = %q", buf.String())
		}
	})
}

var _ fm.Files = (*fakeFiles)(nil)
