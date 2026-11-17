package web

// 缓存生命周期测试：缓存文件在删文件/删卷/覆盖写时必须跟着消失，
// 不允许孤儿堆积。布局：thumbs/<vol>/<路径哈希>-<kind>-<版本>。

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeFileForTest(t *testing.T, path string, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// PurgeEntry 只删同一路径的缓存，别的路径不动。
func TestPurgeEntryRemovesOnlyThatPath(t *testing.T) {
	dir := t.TempDir()
	keep := posterPath(dir, "demo", "/keep.txt", "111")
	kill := posterPath(dir, "demo", "/kill.txt", "222")
	other := posterPath(dir, "demo2", "/kill.txt", "222")
	writeFileForTest(t, keep, "k")
	writeFileForTest(t, kill, "k")
	writeFileForTest(t, other, "o")

	removed := PurgeEntry(dir, "demo", "/kill.txt")
	if removed != 1 {
		t.Fatalf("removed = %d; want 1", removed)
	}
	if _, err := os.Stat(kill); !os.IsNotExist(err) {
		t.Fatal("目标路径的缓存应被删除")
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatal("别的路径缓存不应被动")
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatal("别的卷缓存不应被动")
	}
}

// SweepFlatLegacy 只清根目录下的旧 flat 文件，子目录与新文件不动。
func TestSweepFlatLegacyRemovesOnlyStaleFlatFiles(t *testing.T) {
	dir := t.TempDir()
	oldFlat := filepath.Join(dir, "poster-deadbeef-0.jpg")
	newFlat := filepath.Join(dir, "poster-fresh-0.jpg")
	inSub := posterPath(dir, "demo", "/v.mp4", "0")
	writeFileForTest(t, oldFlat, "old")
	writeFileForTest(t, newFlat, "new")
	writeFileForTest(t, inSub, "sub")

	past := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(oldFlat, past, past); err != nil {
		t.Fatal(err)
	}

	removed := SweepFlatLegacy(dir, time.Hour)
	if removed != 1 {
		t.Fatalf("removed = %d; want 1", removed)
	}
	if _, err := os.Stat(oldFlat); !os.IsNotExist(err) {
		t.Fatal("旧 flat 文件应被清掉")
	}
	if _, err := os.Stat(newFlat); err != nil {
		t.Fatal("新 flat 文件不应被清")
	}
	if _, err := os.Stat(inSub); err != nil {
		t.Fatal("子目录里的缓存不应被清")
	}
}

// posterPath 的 v 参数必须收敛成数字，防路径注入；非法/空版本回落 0。
func TestPosterPathSanitizesVersion(t *testing.T) {
	dir := t.TempDir()
	safe := posterPath(dir, "demo", "/v.mp4", "123")
	if filepath.Dir(safe) != filepath.Join(dir, "demo") {
		t.Fatalf("safe 路径越界: %s", safe)
	}
	evil := posterPath(dir, "demo", "/v.mp4", "../../evil")
	if filepath.Dir(evil) != filepath.Join(dir, "demo") {
		t.Fatalf("evil 版本号应回落 0，不得越界: %s", evil)
	}
	zero := posterPath(dir, "demo", "/v.mp4", "0")
	if evil != zero {
		t.Fatalf("非法版本号应与 0 同路径: %s vs %s", evil, zero)
	}
	empty := posterPath(dir, "demo", "/v.mp4", "")
	if empty != zero {
		t.Fatalf("空版本号应与 0 同路径: %s", empty)
	}
}

// thumbCache.save 覆盖写时清掉同路径旧键：覆盖不留孤儿。
func TestThumbCacheSaveCleansStaleKeys(t *testing.T) {
	c := newThumbCache(t.TempDir())
	stale := []byte{1}
	fresh := []byte{2}

	c.save("demo", "/v.mp4", "key-v1-mtime-a", stale)
	if got := c.load("demo", "/v.mp4", "key-v1-mtime-a"); got == nil {
		t.Fatal("旧键应可读")
	}
	c.save("demo", "/v.mp4", "key-v2-mtime-b", fresh)
	if got := c.load("demo", "/v.mp4", "key-v2-mtime-b"); got == nil {
		t.Fatal("新键应可读")
	}
	if got := c.load("demo", "/v.mp4", "key-v1-mtime-a"); got != nil {
		t.Fatal("覆盖写后旧键应被清掉")
	}
}
