package uploadcache

// 磁盘断点存储的单测：不联网，只验证读写与容错。

import (
	"os"
	"path/filepath"
	"testing"

	"mytgfs/internal/telegramstore"
)

// TestSaveLoadClear 正常写入、读取、删除。
func TestSaveLoadClear(t *testing.T) {
	dir := t.TempDir()
	s := New(filepath.Join(dir, "uploads"))
	in := &telegramstore.UploadResume{FileID: 123, PartSize: 512 * 1024, TotalParts: 4, Done: []int{0, 2}}
	if err := s.Save("abc", in); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load("abc")
	if err != nil || got == nil {
		t.Fatalf("读取失败: %v", err)
	}
	if got.FileID != 123 || len(got.Done) != 2 || got.Done[1] != 2 {
		t.Fatalf("内容不符: %+v", got)
	}
	if err := s.Clear("abc"); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Load("abc"); err != nil || got != nil {
		t.Fatalf("删除后应读不到: %v %+v", err, got)
	}
	// 删两次不应报错
	if err := s.Clear("abc"); err != nil {
		t.Fatalf("重复删除应幂等: %v", err)
	}
}

// TestLoadCorruptReturnsNil 文件损坏时当作没有断点，避免卡住上传。
func TestLoadCorruptReturnsNil(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	if err := os.WriteFile(filepath.Join(dir, "bad.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load("bad")
	if err != nil || got != nil {
		t.Fatalf("损坏文件应返回 nil: %v %+v", err, got)
	}
}

// TestPathSanitizesKey key 里的路径字符不会造成子目录。
func TestPathSanitizesKey(t *testing.T) {
	s := New(t.TempDir())
	if got := s.path("../../etc/passwd"); filepath.Dir(got) != s.dir {
		t.Fatalf("路径越界: %s", got)
	}
}
