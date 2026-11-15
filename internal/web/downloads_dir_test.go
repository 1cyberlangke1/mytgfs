package web

import (
	"os"
	"path/filepath"
	"testing"
)

// 回归背景：下载目录启动时解析一次，面板改 downloads.dir 后不生效。
// dirFn 每次操作现取目录：改配置立即切到新目录，不需要重启。
func TestDownloadStoreFollowsDirChange(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()
	current := dirA
	dl := newDownloadStore(func() string { return current }, nil, nil)

	if err := os.WriteFile(filepath.Join(dirA, "a.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	files, err := dl.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Name != "a.txt" {
		t.Fatalf("目录 A 应列出 a.txt，实际 %v", files)
	}

	// 面板改了 downloads.dir：下一次操作即用新目录
	current = dirB
	files, err = dl.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Fatalf("新目录应为空，实际 %v", files)
	}
	if _, err := dl.Path("a.txt"); err == nil {
		t.Fatal("切换后 Path 不应再解析到旧目录的文件")
	}
	if err := os.WriteFile(filepath.Join(dirB, "b.txt"), []byte("world"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := dl.Remove("b.txt"); err != nil {
		t.Fatalf("Remove 应作用于目录 B: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dirB, "b.txt")); !os.IsNotExist(err) {
		t.Fatal("b.txt 应已从目录 B 删除")
	}
	if _, err := os.Stat(filepath.Join(dirA, "a.txt")); err != nil {
		t.Fatalf("目录 A 的旧文件不应被动过: %v", err)
	}
}
