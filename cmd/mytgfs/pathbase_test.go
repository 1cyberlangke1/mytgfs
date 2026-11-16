package main

// 全局基准目录的单测：--dir / MYTGFS_DIR / 绝对路径三条分支。

import "testing"

// TestExtractDirFlag --dir 的两种写法都被取走，其余参数原样保留。
func TestExtractDirFlag(t *testing.T) {
	t.Cleanup(func() { globalDir = "" })
	cases := []struct {
		args []string
		want []string
		dir  string
	}{
		{args: []string{"ls"}, want: []string{"ls"}, dir: ""},
		{args: []string{"--dir", "/a", "ls"}, want: []string{"ls"}, dir: "/a"},
		{args: []string{"--dir=/b", "ls", "x"}, want: []string{"ls", "x"}, dir: "/b"},
		{args: []string{"ls", "--dir", "/c"}, want: []string{"ls"}, dir: "/c"},
	}
	for _, c := range cases {
		globalDir = ""
		got := extractDirFlag(c.args)
		if c.dir != globalDir {
			t.Errorf("extractDirFlag(%v) 的 --dir = %q，期望 %q", c.args, globalDir, c.dir)
		}
		if len(got) != len(c.want) {
			t.Fatalf("extractDirFlag(%v) = %v，期望 %v", c.args, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("extractDirFlag(%v)[%d] = %q，期望 %q", c.args, i, got[i], c.want[i])
			}
		}
	}
}

// TestVolPathBase 相对路径拼在基准目录之后，绝对路径不受影响。
func TestVolPathBase(t *testing.T) {
	t.Cleanup(func() {
		globalDir = ""
		t.Setenv("MYTGFS_DIR", "")
	})
	cases := []struct {
		global string
		env    string
		in     string
		want   string
	}{
		{in: "a.txt", want: "/a.txt"},
		{global: "/work", in: "a.txt", want: "/work/a.txt"},
		{global: "/work/", in: "sub/a.txt", want: "/work/sub/a.txt"},
		{env: "/env", in: "a.txt", want: "/env/a.txt"},
		// --dir 优先于环境变量
		{global: "/work", env: "/env", in: "a.txt", want: "/work/a.txt"},
		// 绝对路径不拼接
		{global: "/work", in: "/abs/a.txt", want: "/abs/a.txt"},
		// 空路径落到基准目录本身
		{global: "/work", in: "", want: "/work"},
		// Windows 分隔符同样拼接
		{global: "/work", in: `sub\b.txt`, want: "/work/sub/b.txt"},
	}
	for _, c := range cases {
		globalDir = c.global
		t.Setenv("MYTGFS_DIR", c.env)
		if got := volPath(c.in); got != c.want {
			t.Errorf("volPath(%q) --dir=%q env=%q = %q，期望 %q", c.in, c.global, c.env, got, c.want)
		}
	}
}

// TestBaseDir 基准目录默认是卷根，且总以 / 开头。
func TestBaseDir(t *testing.T) {
	t.Cleanup(func() {
		globalDir = ""
		t.Setenv("MYTGFS_DIR", "")
	})
	globalDir = ""
	t.Setenv("MYTGFS_DIR", "")
	if got := baseDir(); got != "/" {
		t.Errorf("默认 baseDir() = %q，期望 /", got)
	}
	globalDir = `work\sub`
	if got := baseDir(); got != "/work/sub" {
		t.Errorf("baseDir() = %q，期望 /work/sub", got)
	}
}
