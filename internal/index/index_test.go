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

// TestPendingRoundtrip 待补传表按 path 覆盖写入、可列出、可删除。
//
// 输入：同一路径写两次不同的临时文件，再对另一路径写一条
// 输出：ListPending 的结果
// 预期：同路径只保留后一条；删除后只剩另一条；删除不存在的路径不报错
func TestPendingRoundtrip(t *testing.T) {
	ctx := context.Background()
	idx, err := OpenMemory()
	if err != nil {
		t.Fatalf("打开索引: %v", err)
	}
	defer idx.Close()

	if err := idx.PutPending(ctx, PendingUpload{Path: "/a.txt", Temp: "t1", Size: 1, Mtime: 1}); err != nil {
		t.Fatalf("PutPending: %v", err)
	}
	if err := idx.PutPending(ctx, PendingUpload{Path: "/a.txt", Temp: "t2", Size: 2, Mtime: 2}); err != nil {
		t.Fatalf("PutPending 覆盖: %v", err)
	}
	if err := idx.PutPending(ctx, PendingUpload{Path: "/b.txt", Temp: "t3", Size: 3, Mtime: 3}); err != nil {
		t.Fatalf("PutPending b: %v", err)
	}

	rows, err := idx.ListPending(ctx)
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(rows) != 2 || rows[0].Path != "/a.txt" || rows[0].Temp != "t2" || rows[1].Path != "/b.txt" {
		t.Fatalf("待补传记录不符: %+v", rows)
	}

	if err := idx.DeletePending(ctx, "/a.txt"); err != nil {
		t.Fatalf("DeletePending: %v", err)
	}
	if err := idx.DeletePending(ctx, "/nope.txt"); err != nil {
		t.Fatalf("删除不存在的记录不应报错: %v", err)
	}
	rows, err = idx.ListPending(ctx)
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(rows) != 1 || rows[0].Path != "/b.txt" {
		t.Fatalf("删除后剩余记录不符: %+v", rows)
	}
}

// TestSearchMatchesBaseName 只匹配最后一段名字，父目录名不算命中。
func TestSearchMatchesBaseName(t *testing.T) {
	idx := newTestIndex(t)
	ctx := context.Background()
	for _, f := range []File{
		{Path: "/photos/report.txt", MessageID: 1, Size: 10},
		{Path: "/reports/a.txt", MessageID: 2, Size: 20},
		{Path: "/docs/note.md", MessageID: 3, Size: 30},
	} {
		if err := idx.Put(ctx, f); err != nil {
			t.Fatal(err)
		}
	}
	if err := idx.PutDir(ctx, "/docs/empty"); err != nil {
		t.Fatal(err)
	}

	hits, err := idx.Search(ctx, "/", "REPORT", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Path != "/photos/report.txt" {
		t.Fatalf("应只命中文件名: %+v", hits)
	}

	hits, err = idx.Search(ctx, "/", "empty", 0)
	if err != nil || len(hits) != 1 || !hits[0].IsDir {
		t.Fatalf("显式目录也应命中: %+v %v", hits, err)
	}

	if hits, err = idx.Search(ctx, "/", "  ", 0); err != nil || len(hits) != 0 {
		t.Fatalf("空查询应返回空: %+v %v", hits, err)
	}

	hits, err = idx.Search(ctx, "/docs", "a", 1)
	if err != nil || len(hits) > 1 {
		t.Fatalf("limit 未生效: %+v %v", hits, err)
	}
}
