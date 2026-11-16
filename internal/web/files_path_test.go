package web

// 上传落点计算的回归测试。
//
// 输入：dir / rel / name 各种组合，含改名场景；
// 输出：断言 uploadTarget 的落点与错误；
// 预期：面板在上传前改名时，目录内的文件必须把新名字写进 rel——
//       因为 rel 非空时后端只认 rel，name 会被忽略。

import (
	"errors"
	"testing"

	"mytgfs/internal/fm"
)

// TestUploadTarget 覆盖落点计算的正常与非法输入。
func TestUploadTarget(t *testing.T) {
	cases := []struct {
		name     string
		dir      string
		rel      string
		fileName string
		want     string
		wantErr  bool
	}{
		{name: "根目录下按 name 落盘", dir: "/", rel: "", fileName: "a.txt", want: "/a.txt"},
		{name: "子目录下按 name 落盘", dir: "/sub", rel: "", fileName: "a.txt", want: "/sub/a.txt"},
		{name: "name 两端空白被去掉", dir: "/", rel: "", fileName: "  a.txt  ", want: "/a.txt"},
		{name: "空 name 被拒", dir: "/", rel: "", fileName: "", wantErr: true},
		{name: "点目录被拒", dir: "/", rel: "", fileName: "..", wantErr: true},
		{name: "name 带分隔符被拒", dir: "/", rel: "", fileName: "a/b.txt", wantErr: true},

		// 改名语义：rel 非空时 name 完全不参与计算
		{name: "rel 非空时忽略 name", dir: "/", rel: "dir/b.txt", fileName: "ignored.txt", want: "/dir/b.txt"},
		{name: "rel 带反斜杠也能规范化", dir: "/", rel: `dir\sub\c.txt`, fileName: "", want: "/dir/sub/c.txt"},
		{name: "rel 结尾是目录被拒", dir: "/", rel: "dir/", fileName: "b.txt", wantErr: true},
		{name: "rel 含 .. 被拒", dir: "/", rel: "dir/../x.txt", fileName: "b.txt", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := uploadTarget(tc.dir, tc.rel, tc.fileName)
			if tc.wantErr {
				if !errors.Is(err, fm.ErrBadPath) {
					t.Fatalf("uploadTarget(%q,%q,%q) err = %v, want ErrBadPath", tc.dir, tc.rel, tc.fileName, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("uploadTarget(%q,%q,%q) unexpected err = %v", tc.dir, tc.rel, tc.fileName, err)
			}
			if got != tc.want {
				t.Fatalf("uploadTarget(%q,%q,%q) = %q, want %q", tc.dir, tc.rel, tc.fileName, got, tc.want)
			}
		})
	}
}

// TestSafeName 覆盖文件名校验。
func TestSafeName(t *testing.T) {
	cases := map[string]string{
		"a.txt":     "a.txt",
		"  a.txt  ": "a.txt",
		"a/b.txt":   "",
		`a\b.txt`:   "",
		"":          "",
		".":         "",
		"..":        "",
	}
	for in, want := range cases {
		if got := safeName(in); got != want {
			t.Fatalf("safeName(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestCleanRelPath 覆盖相对路径规范化。
func TestCleanRelPath(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"a/b.txt", "a/b.txt", true},
		{"a//b/./c.txt", "a/b/c.txt", true},
		{"/abs/b.txt", "abs/b.txt", true},
		{`a\b\c.txt`, "a/b/c.txt", true},
		{"../x.txt", "", false},
		{"", "", false},
	}
	for _, tc := range cases {
		got, ok := cleanRelPath(tc.in)
		if ok != tc.ok || got != tc.want {
			t.Fatalf("cleanRelPath(%q) = (%q,%v), want (%q,%v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}
