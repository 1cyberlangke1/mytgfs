package main

// shell 的纯逻辑单测：命令切分、路径解析、退出命令。

import (
	"errors"
	"testing"
)

// TestSplitArgs 空白切分与双引号包裹。
func TestSplitArgs(t *testing.T) {
	cases := []struct {
		line string
		want []string
	}{
		{line: "", want: nil},
		{line: "ls", want: []string{"ls"}},
		{line: "  ls   /a  ", want: []string{"ls", "/a"}},
		{line: `cd "a b"`, want: []string{"cd", "a b"}},
		{line: `get "x y.mp4" out.mp4`, want: []string{"get", "x y.mp4", "out.mp4"}},
		{line: `put "a b" c`, want: []string{"put", "a b", "c"}},
	}
	for _, c := range cases {
		got := splitArgs(c.line)
		if len(got) != len(c.want) {
			t.Fatalf("splitArgs(%q) = %v，期望 %v", c.line, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("splitArgs(%q)[%d] = %q，期望 %q", c.line, i, got[i], c.want[i])
			}
		}
	}
}

// TestShellResolve 相对路径拼当前目录，绝对路径归一。
func TestShellResolve(t *testing.T) {
	sh := &shell{cwd: "/work"}
	cases := map[string]string{
		"":           "/work",
		"a.txt":      "/work/a.txt",
		"/abs/a":     "/abs/a",
		`sub\b.txt`:  "/work/sub/b.txt",
		"..":         "/",
		"/work/../x": "/x",
	}
	for in, want := range cases {
		if got := sh.resolve(in); got != want {
			t.Errorf("resolve(%q) = %q，期望 %q", in, got, want)
		}
	}
}

// TestShellQuit quit/exit 返回专用错误，未知命令给出普通错误。
func TestShellQuit(t *testing.T) {
	sh := &shell{}
	for _, cmd := range []string{"quit", "exit"} {
		if err := sh.dispatch([]string{cmd}); !errors.Is(err, errShellQuit) {
			t.Errorf("%s 应返回 errShellQuit，得到 %v", cmd, err)
		}
	}
	err := sh.dispatch([]string{"nope"})
	if err == nil || errors.Is(err, errShellQuit) {
		t.Fatalf("未知命令应返回普通错误，得到 %v", err)
	}
}
