package datadir

import (
	"os"
	"path/filepath"
	"testing"
)

// TestPathCreatesParentDir 运行目录必须自动建好，调用方不必再 MkdirAll。
//
// 输入：当前目录切到临时目录，请求一个带子目录的文件名
// 输出：返回 data/ 下的相对路径；目录已存在
func TestPathCreatesParentDir(t *testing.T) {
	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })

	got := Path(filepath.Join("uploads", "a.json"))
	want := filepath.Join(Dir, "uploads", "a.json")
	if got != want {
		t.Fatalf("路径 = %q，期望 %q", got, want)
	}
	st, err := os.Stat(filepath.Join(Dir, "uploads"))
	if err != nil {
		t.Fatalf("目录未创建: %v", err)
	}
	if !st.IsDir() {
		t.Fatal("创建出来的不是目录")
	}
}
