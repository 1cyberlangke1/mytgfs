package main

// rm / mv / cat 命令层逻辑的单测：验证失败时已处理部分如实保留、
// 目标目录补名规则、以及多文件拼接顺序。

import (
	"bytes"
	"errors"
	"os"
	"testing"

	"mytgfs/internal/fm"
)

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
