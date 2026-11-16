package app

// channelCache 的本地持久化：写进去能读出、删掉后不再命中、没有索引时静默跳过。

import (
	"path/filepath"
	"testing"

	"mytgfs/internal/index"
)

// TestChannelCacheRoundTrip 写→读→删。
func TestChannelCacheRoundTrip(t *testing.T) {
	idx, err := index.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatalf("index.Open: %v", err)
	}
	t.Cleanup(func() { _ = idx.Close() })
	c := channelCache{idx: idx}

	if err := c.SaveAccessHash(t.Context(), 42, 777); err != nil {
		t.Fatalf("Save: %v", err)
	}
	hash, ok, err := c.LoadAccessHash(t.Context(), 42)
	if err != nil || !ok || hash != 777 {
		t.Fatalf("Load = %d/%v err=%v，期望 777/true", hash, ok, err)
	}

	if err := c.DropAccessHash(t.Context(), 42); err != nil {
		t.Fatalf("Drop: %v", err)
	}
	if _, ok, err := c.LoadAccessHash(t.Context(), 42); err != nil || ok {
		t.Fatalf("删除后仍命中: ok=%v err=%v", ok, err)
	}
}

// TestChannelCacheIgnoresZeroAndNil 0 与空索引都视为未命中。
func TestChannelCacheIgnoresZeroAndNil(t *testing.T) {
	if err := (channelCache{}).SaveAccessHash(t.Context(), 42, 1); err != nil {
		t.Fatalf("空索引 Save 应静默: %v", err)
	}
	if _, ok, _ := (channelCache{}).LoadAccessHash(t.Context(), 42); ok {
		t.Fatal("空索引不该命中")
	}

	idx, err := index.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatalf("index.Open: %v", err)
	}
	t.Cleanup(func() { _ = idx.Close() })
	c := channelCache{idx: idx}
	if err := c.SaveAccessHash(t.Context(), 42, 0); err != nil {
		t.Fatalf("Save 0: %v", err)
	}
	if _, ok, _ := c.LoadAccessHash(t.Context(), 42); ok {
		t.Fatal("access_hash 为 0 不该命中")
	}
}
