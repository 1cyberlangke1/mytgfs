package index

import (
	"context"
	"testing"
)

func newTestIndex(t *testing.T) *Index {
	t.Helper()
	idx, err := OpenMemory()
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { idx.Close() })
	return idx
}

func TestPutGet(t *testing.T) {
	idx := newTestIndex(t)
	ctx := context.Background()

	f := File{Path: "/a.txt", MessageID: 1, DocumentID: 2, AccessHash: 3, Size: 4, Mtime: 5}
	if err := idx.Put(ctx, f); err != nil {
		t.Fatalf("put: %v", err)
	}

	got, ok, err := idx.Get(ctx, "/a.txt")
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if got.MessageID != 1 || got.DocumentID != 2 || got.Size != 4 || got.Mtime != 5 {
		t.Fatalf("unexpected: %+v", got)
	}
}

func TestPutLatestWins(t *testing.T) {
	idx := newTestIndex(t)
	ctx := context.Background()

	if err := idx.Put(ctx, File{Path: "/a.txt", MessageID: 2, Size: 20}); err != nil {
		t.Fatal(err)
	}
	// 旧版本不应覆盖新版本
	if err := idx.Put(ctx, File{Path: "/a.txt", MessageID: 1, Size: 10}); err != nil {
		t.Fatal(err)
	}

	got, _, err := idx.Get(ctx, "/a.txt")
	if err != nil {
		t.Fatal(err)
	}
	if got.MessageID != 2 || got.Size != 20 {
		t.Fatalf("latest should win: %+v", got)
	}
}

func TestListDerivesDirectories(t *testing.T) {
	idx := newTestIndex(t)
	ctx := context.Background()

	_ = idx.Put(ctx, File{Path: "/docs/a.txt", MessageID: 1})
	_ = idx.Put(ctx, File{Path: "/docs/sub/b.txt", MessageID: 2})
	_ = idx.Put(ctx, File{Path: "/root.txt", MessageID: 3})

	root, err := idx.List(ctx, "/")
	if err != nil {
		t.Fatal(err)
	}
	if !hasEntry(root, "docs", true) || !hasEntry(root, "root.txt", false) {
		t.Fatalf("root entries: %+v", root)
	}
	// sub 不应出现在根目录
	if hasEntry(root, "sub", true) {
		t.Fatalf("nested dir leaked into root: %+v", root)
	}

	docs, err := idx.List(ctx, "/docs")
	if err != nil {
		t.Fatal(err)
	}
	if !hasEntry(docs, "a.txt", false) || !hasEntry(docs, "sub", true) {
		t.Fatalf("docs entries: %+v", docs)
	}
}

func TestDelete(t *testing.T) {
	idx := newTestIndex(t)
	ctx := context.Background()

	_ = idx.Put(ctx, File{Path: "/a.txt", MessageID: 1})
	if err := idx.Delete(ctx, "/a.txt"); err != nil {
		t.Fatal(err)
	}
	_, ok, err := idx.Get(ctx, "/a.txt")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("record should be deleted")
	}
}

// TestDeleteByMessageID 按消息号删除，且不影响其他记录。
func TestDeleteByMessageID(t *testing.T) {
	idx := newTestIndex(t)
	ctx := context.Background()

	_ = idx.Put(ctx, File{Path: "/a.txt", MessageID: 11})
	_ = idx.Put(ctx, File{Path: "/b.txt", MessageID: 12})

	if err := idx.DeleteByMessageID(ctx, 11); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := idx.Get(ctx, "/a.txt"); ok {
		t.Fatal("消息 11 对应的记录应被删除")
	}
	if _, ok, _ := idx.Get(ctx, "/b.txt"); !ok {
		t.Fatal("消息 12 不应受影响")
	}
	// 幂等：不存在的消息号不算错误
	if err := idx.DeleteByMessageID(ctx, 999); err != nil {
		t.Fatalf("幂等删除报错: %v", err)
	}
	if err := idx.DeleteByMessageID(ctx, 0); err != nil {
		t.Fatalf("0 消息号应直接忽略: %v", err)
	}
}

// TestMeta 持久化整数状态，缺失时返回 false 而不是错误。
func TestMeta(t *testing.T) {
	idx := newTestIndex(t)
	ctx := context.Background()

	if v, ok, err := idx.GetMeta(ctx, MetaKeyChannelPts); err != nil || ok || v != 0 {
		t.Fatalf("缺失状态应返回 (0,false,nil)，实际 (%d,%v,%v)", v, ok, err)
	}
	if err := idx.PutMeta(ctx, MetaKeyChannelPts, 55); err != nil {
		t.Fatal(err)
	}
	if v, ok, err := idx.GetMeta(ctx, MetaKeyChannelPts); err != nil || !ok || v != 55 {
		t.Fatalf("读回失败: (%d,%v,%v)", v, ok, err)
	}
	if err := idx.PutMeta(ctx, MetaKeyChannelPts, 77); err != nil {
		t.Fatal(err)
	}
	if v, _, _ := idx.GetMeta(ctx, MetaKeyChannelPts); v != 77 {
		t.Fatalf("覆盖写失败: %d", v)
	}
}

func hasEntry(entries []Entry, name string, isDir bool) bool {
	for _, e := range entries {
		if e.Name == name && e.IsDir == isDir {
			return true
		}
	}
	return false
}
