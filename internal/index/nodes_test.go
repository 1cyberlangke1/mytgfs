package index

// 节点索引单测：写入覆盖、改名/移动、子树删除、防环、内容去重、路径搜索。

import (
	"context"
	"testing"
)

func newTestIndex(t *testing.T) *Index {
	t.Helper()
	idx, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	t.Cleanup(func() { _ = idx.Close() })
	return idx
}

// TestNodeRoundTrip 写入后按 id / path / 父目录 + 名字都能取回。
func TestNodeRoundTrip(t *testing.T) {
	ctx := context.Background()
	idx := newTestIndex(t)
	n := Node{ID: 7, ParentID: 0, Name: "a.txt", Path: "/a.txt", Size: 10, Sha256: "ab"}
	if err := idx.PutNode(ctx, n); err != nil {
		t.Fatalf("PutNode: %v", err)
	}
	got, ok, err := idx.NodeByID(ctx, 7)
	if err != nil || !ok || got.Sha256 != "ab" || got.Size != 10 {
		t.Fatalf("NodeByID=(%+v,%v,%v)", got, ok, err)
	}
	if _, ok, _ := idx.NodeByPath(ctx, "/a.txt"); !ok {
		t.Fatal("NodeByPath 没找到")
	}
	if _, ok, _ := idx.Child(ctx, 0, "a.txt"); !ok {
		t.Fatal("Child 没找到")
	}
}

// TestPutNodeSameNameReplaces 同 (parent,name) 用新消息号顶掉旧行。
func TestPutNodeSameNameReplaces(t *testing.T) {
	ctx := context.Background()
	idx := newTestIndex(t)
	_ = idx.PutNode(ctx, Node{ID: 1, Name: "a.txt"})
	_ = idx.PutNode(ctx, Node{ID: 2, Name: "a.txt", Size: 5})
	got, ok, _ := idx.NodeByPath(ctx, "/a.txt")
	if !ok || got.ID != 2 || got.Size != 5 {
		t.Fatalf("覆盖结果=(%+v,%v)", got, ok)
	}
}

// TestChildrenOrder 目录优先、名字升序。
func TestChildrenOrder(t *testing.T) {
	ctx := context.Background()
	idx := newTestIndex(t)
	_ = idx.PutNode(ctx, Node{ID: 3, Name: "z.txt"})
	_ = idx.PutNode(ctx, Node{ID: 2, Name: "sub", IsDir: true})
	_ = idx.PutNode(ctx, Node{ID: 1, Name: "a.txt"})
	children, err := idx.Children(ctx, 0)
	if err != nil || len(children) != 3 {
		t.Fatalf("Children=(%v,%v)", children, err)
	}
	if children[0].Name != "sub" || children[1].Name != "a.txt" || children[2].Name != "z.txt" {
		t.Fatalf("顺序=%v", children)
	}
}

// TestMoveNodeUpdatesSubtree 改名/换父时整棵子树路径一次更新。
func TestMoveNodeUpdatesSubtree(t *testing.T) {
	ctx := context.Background()
	idx := newTestIndex(t)
	_ = idx.PutNode(ctx, Node{ID: 1, Name: "dir", IsDir: true})
	_ = idx.PutNode(ctx, Node{ID: 2, ParentID: 1, Name: "f.txt", Path: "/dir/f.txt"})
	_ = idx.PutNode(ctx, Node{ID: 3, Name: "dst", IsDir: true})
	if _, _, err := idx.MoveNode(ctx, 1, 3, "renamed"); err != nil {
		t.Fatalf("MoveNode: %v", err)
	}
	n, ok, _ := idx.NodeByID(ctx, 2)
	if !ok || n.Path != "/dst/renamed/f.txt" {
		t.Fatalf("子树路径=%+v ok=%v", n, ok)
	}
}

// TestMoveNodeRejectsCycle 目录不能移进自己的子树。
func TestMoveNodeRejectsCycle(t *testing.T) {
	ctx := context.Background()
	idx := newTestIndex(t)
	_ = idx.PutNode(ctx, Node{ID: 1, Name: "a", IsDir: true})
	_ = idx.PutNode(ctx, Node{ID: 2, ParentID: 1, Name: "b", IsDir: true, Path: "/a/b"})
	if _, _, err := idx.MoveNode(ctx, 1, 2, "a"); err == nil {
		t.Fatal("移进自己子树应该报错")
	}
}

// TestIsDescendant 沿 parent 链判定，成环时安全停下。
func TestIsDescendant(t *testing.T) {
	ctx := context.Background()
	idx := newTestIndex(t)
	_ = idx.PutNode(ctx, Node{ID: 1, Name: "a", IsDir: true})
	_ = idx.PutNode(ctx, Node{ID: 2, ParentID: 1, Name: "b"})
	_ = idx.PutNode(ctx, Node{ID: 3, ParentID: 2, Name: "c"})
	if ok, _ := idx.IsDescendant(ctx, 1, 3); !ok {
		t.Fatal("3 应该是 1 的后代")
	}
	if ok, _ := idx.IsDescendant(ctx, 3, 1); ok {
		t.Fatal("1 不是 3 的后代")
	}
	// 人为成环：2 的父指向 3
	_ = idx.PutNode(ctx, Node{ID: 2, ParentID: 3, Name: "b", Path: "/a/b"})
	if ok, _ := idx.IsDescendant(ctx, 1, 3); ok {
		t.Fatal("成环时不应误判")
	}
}

// TestDeleteSubtree 删目录连后代一起删，返回全部消息号。
func TestDeleteSubtree(t *testing.T) {
	ctx := context.Background()
	idx := newTestIndex(t)
	_ = idx.PutNode(ctx, Node{ID: 1, Name: "dir", IsDir: true})
	_ = idx.PutNode(ctx, Node{ID: 2, ParentID: 1, Name: "f.txt", Path: "/dir/f.txt"})
	ids, err := idx.DeleteSubtree(ctx, 1)
	if err != nil || len(ids) != 2 {
		t.Fatalf("DeleteSubtree=(%v,%v)", ids, err)
	}
	if _, ok, _ := idx.NodeByID(ctx, 2); ok {
		t.Fatal("后代没删干净")
	}
}

// TestFindContent 同内容命中；sha 为空不命中。
func TestFindContent(t *testing.T) {
	ctx := context.Background()
	idx := newTestIndex(t)
	_ = idx.PutNode(ctx, Node{ID: 5, Name: "a.bin", Size: 3, Sha256: "abc"})
	if _, ok, _ := idx.FindContent(ctx, "/", 3, "abc"); !ok {
		t.Fatal("同摘要同大小应命中")
	}
	if _, ok, _ := idx.FindContent(ctx, "/", 3, ""); ok {
		t.Fatal("空摘要不该命中")
	}
	if _, ok, _ := idx.FindContent(ctx, "/", 4, "abc"); ok {
		t.Fatal("大小不符不该命中")
	}
}

// TestCountRefs 同一 document 有多条路径引用。
func TestCountRefs(t *testing.T) {
	ctx := context.Background()
	idx := newTestIndex(t)
	_ = idx.PutNode(ctx, Node{ID: 1, Name: "a.bin", DocumentID: 99})
	_ = idx.PutNode(ctx, Node{ID: 2, Name: "b.bin", DocumentID: 99})
	n, _ := idx.CountRefs(ctx, 99)
	if n != 2 {
		t.Fatalf("CountRefs=%d", n)
	}
}

// TestUsedCountsFiles 已用空间只统计文件，不算目录。
func TestUsedCountsFiles(t *testing.T) {
	ctx := context.Background()
	idx := newTestIndex(t)
	_ = idx.PutNode(ctx, Node{ID: 1, Name: "dir", IsDir: true, Size: 999})
	_ = idx.PutNode(ctx, Node{ID: 2, ParentID: 1, Name: "a.bin", Size: 100, Path: "/dir/a.bin"})
	_ = idx.PutNode(ctx, Node{ID: 3, ParentID: 1, Name: "b.bin", Size: 22, Path: "/dir/b.bin"})
	used, err := idx.Used(ctx)
	if err != nil || used != 122 {
		t.Fatalf("Used=(%d,%v)", used, err)
	}
	if n, _ := idx.CountFiles(ctx); n != 2 {
		t.Fatalf("CountFiles=%d", n)
	}
}

// TestSearchMatchesBaseName 只匹配最后一段名字，跨目录命中带全路径。
func TestSearchMatchesBaseName(t *testing.T) {
	ctx := context.Background()
	idx := newTestIndex(t)
	_ = idx.PutNode(ctx, Node{ID: 1, Name: "sub", IsDir: true})
	_ = idx.PutNode(ctx, Node{ID: 2, ParentID: 1, Name: "report.txt", Path: "/sub/report.txt"})
	hits, err := idx.Search(ctx, "/", "REPORT", 0)
	if err != nil || len(hits) != 1 || hits[0].Path != "/sub/report.txt" {
		t.Fatalf("Search=(%v,%v)", hits, err)
	}
	if got, _ := idx.Search(ctx, "/", "", 0); len(got) != 0 {
		t.Fatal("空 query 不该返回命中")
	}
}

// TestListPrefixRootBoundaries /sub 不该把 /subway.txt 算进来。
func TestListPrefixRootBoundaries(t *testing.T) {
	ctx := context.Background()
	idx := newTestIndex(t)
	_ = idx.PutNode(ctx, Node{ID: 1, Name: "sub", IsDir: true})
	_ = idx.PutNode(ctx, Node{ID: 2, ParentID: 1, Name: "f.txt", Path: "/sub/f.txt"})
	_ = idx.PutNode(ctx, Node{ID: 3, Name: "subway.txt"})
	got, err := idx.ListPrefix(ctx, "/sub")
	if err != nil || len(got) != 1 || got[0].ID != 2 {
		t.Fatalf("ListPrefix=(%v,%v)", got, err)
	}
}

// TestPathByMessageID 按消息号反查路径。
func TestPathByMessageID(t *testing.T) {
	ctx := context.Background()
	idx := newTestIndex(t)
	_ = idx.PutNode(ctx, Node{ID: 9, Name: "a.txt"})
	if p, ok, _ := idx.PathByMessageID(ctx, 9); !ok || p != "/a.txt" {
		t.Fatalf("PathByMessageID=(%q,%v)", p, ok)
	}
	if _, ok, _ := idx.PathByMessageID(ctx, 999); ok {
		t.Fatal("不存在的消息号不该命中")
	}
}

// TestMetaCursor pts 游标读写。
func TestMetaCursor(t *testing.T) {
	ctx := context.Background()
	idx := newTestIndex(t)
	if _, ok, _ := idx.GetMeta(ctx, MetaKeyChannelPts); ok {
		t.Fatal("初始不该有游标")
	}
	_ = idx.PutMeta(ctx, MetaKeyChannelPts, 55)
	if v, ok, _ := idx.GetMeta(ctx, MetaKeyChannelPts); !ok || v != 55 {
		t.Fatalf("GetMeta=(%d,%v)", v, ok)
	}
}

// TestPendingDone 已上传待索引记录的往返与删除。
func TestPendingDone(t *testing.T) {
	ctx := context.Background()
	idx := newTestIndex(t)
	_ = idx.PutPendingDone(ctx, PendingDone{Path: "/a.bin", MessageID: 4, Size: 3})
	p, ok, _ := idx.GetPendingDone(ctx, "/a.bin")
	if !ok || p.MessageID != 4 {
		t.Fatalf("GetPendingDone=(%+v,%v)", p, ok)
	}
	_ = idx.DeletePendingDone(ctx, "/a.bin")
	if _, ok, _ := idx.GetPendingDone(ctx, "/a.bin"); ok {
		t.Fatal("删除后不该还在")
	}
}

// TestDeleteNodeIdempotent 不存在的 id 删除不算错误。
func TestDeleteNodeIdempotent(t *testing.T) {
	idx := newTestIndex(t)
	if err := idx.DeleteNode(context.Background(), 0); err != nil {
		t.Fatalf("DeleteNode(0): %v", err)
	}
	if err := idx.DeleteNode(context.Background(), 999); err != nil {
		t.Fatalf("DeleteNode(999): %v", err)
	}
}
