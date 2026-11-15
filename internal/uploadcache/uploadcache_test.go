package uploadcache

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Sweep 清理过期断点：Telegram 服务端临时区约一天过期，
// 超过时限的断点续不上，留着只会堆积。
func TestSweepRemovesOnlyStaleJson(t *testing.T) {
	dir := t.TempDir()
	stale := filepath.Join(dir, "aaa.json")
	fresh := filepath.Join(dir, "bbb.json")
	notJson := filepath.Join(dir, "ccc.txt")
	for _, f := range []string{stale, fresh, notJson} {
		if err := os.WriteFile(f, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	past := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(stale, past, past); err != nil {
		t.Fatal(err)
	}

	if n := Sweep(dir, 24*time.Hour); n != 1 {
		t.Fatalf("removed = %d; want 1", n)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("过期断点应被删除")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatal("新断点不应被删")
	}
	if _, err := os.Stat(notJson); err != nil {
		t.Fatal("非 .json 文件不应被删")
	}
}

// Sweep 目录不存在时返回 0，不报错。
func TestSweepMissingDir(t *testing.T) {
	if n := Sweep(t.TempDir(), time.Hour); n != 0 {
		t.Fatalf("missing dir removed = %d; want 0", n)
	}
}
