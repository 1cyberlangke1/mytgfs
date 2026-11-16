package main

// getFile 的单测：续传判定、整份重下、目录拒绝、标准输出直出。

import (
	"os"
	"path/filepath"
	"testing"

	"mytgfs/internal/fm"
)

// newGetFiles 构造远端测试数据：/a.bin 十个字节，/dir 是目录。
//
// 输入：无
// 输出：带 info/data 的 fakeFiles
// 预期：getFile 只依赖 Info/Open 两个方法
func newGetFiles() *fakeFiles {
	return &fakeFiles{
		info: map[string]fm.Entry{
			"/a.bin": {Name: "a.bin", Size: 10},
			"/dir":   {Name: "dir", Dir: true},
		},
		data: map[string][]byte{"/a.bin": []byte("0123456789")},
	}
}

// TestGetFileResume 本地文件比远端短时从已有长度续写。
func TestGetFileResume(t *testing.T) {
	local := filepath.Join(t.TempDir(), "a.bin")
	// 续传只追加，不校验既有前缀：本地内容必须是远端前缀
	if err := os.WriteFile(local, []byte("01234"), 0o600); err != nil {
		t.Fatal(err)
	}
	written, resumed, err := getFile(t.Context(), newGetFiles(), "/a.bin", local)
	if err != nil {
		t.Fatalf("getFile: %v", err)
	}
	if written != 10 || resumed != 5 {
		t.Fatalf("written=%d resumed=%d，期望 10 / 5", written, resumed)
	}
	got, err := os.ReadFile(local)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "0123456789" {
		t.Fatalf("内容 = %q，期望完整覆盖", got)
	}
}

// TestGetFileRestarts 本地等长或更长时不续传，整份重下。
func TestGetFileRestarts(t *testing.T) {
	cases := map[string]string{
		"等长": "0123456789",
		"更长": "0123456789abcdef",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			local := filepath.Join(t.TempDir(), "a.bin")
			if err := os.WriteFile(local, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			written, resumed, err := getFile(t.Context(), newGetFiles(), "/a.bin", local)
			if err != nil {
				t.Fatalf("getFile: %v", err)
			}
			if written != 10 || resumed != 0 {
				t.Fatalf("written=%d resumed=%d，期望 10 / 0", written, resumed)
			}
			got, err := os.ReadFile(local)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != "0123456789" {
				t.Fatalf("内容 = %q，期望 0123456789", got)
			}
		})
	}
}

// TestGetFileDir 远端是目录时明确报错。
func TestGetFileDir(t *testing.T) {
	if _, _, err := getFile(t.Context(), newGetFiles(), "/dir", ""); err == nil {
		t.Fatal("目录路径应返回错误")
	}
}

// TestGetFileStdout local 为 "-" 时写标准输出，返回写入字节数。
func TestGetFileStdout(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out.bin")
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = f
	written, resumed, gerr := getFile(t.Context(), newGetFiles(), "/a.bin", "-")
	os.Stdout = old
	if cerr := f.Close(); cerr != nil {
		t.Fatalf("close: %v", cerr)
	}
	if gerr != nil {
		t.Fatalf("getFile: %v", gerr)
	}
	if written != 10 || resumed != 0 {
		t.Fatalf("written=%d resumed=%d，期望 10 / 0", written, resumed)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "0123456789" {
		t.Fatalf("stdout 内容 = %q", got)
	}
}
