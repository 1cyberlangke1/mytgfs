package index

import (
	"context"
	"testing"
)

// newNodes 建一个带根的测试索引。
func newNodes(t *testing.T) (*Index, context.Context) {
	t.Helper()
	idx, err := OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	t.Cleanup(func() { _ = idx.Close() })
	ctx := context.Background()
	if err := idx.SetRoot(ctx, 1); err != nil {
		t.Fatalf("SetRoot: %v", err)
	}
	return idx, ctx
}

// Test 根节点与子节点路径派生。
func TestNodesRootAndChild(t *testing.T) {
	idx, ctx := newNodes(t)

	root, ok, err := idx.Root(ctx)
	if err != nil || !ok {
		t.Fatalf("Root=(%+v,%v,%v)", root, ok, err)
	}
	if root.Path != "/" || !root.IsDir || root.ParentID != 0 {
		t.Fatalf("根节点=%+v", root)
	}

	if err := idx.PutNode(ctx, Node{ID: 10, ParentID: 1, Name: "a.txt", Size: 5, Mtime: 100}); err != nil {
		t.Fatalf("PutNode: %v", err)
	}
	if err := idx.PutNode(ctx, Node{ID: 11, ParentID: 1, Name: "sub", IsDir: true}); err != nil {
		t.Fatalf("PutNode dir: %v", err)
	}
	if err := idx.PutNode(ctx, Node{ID: 12, ParentID: 11, Name: "b.txt", Size: 7}); err != nil {
		t.Fatalf("PutNode nested: %v", err)
	}

	n, ok, err := idx.NodeByPath(ctx, "/a.txt")
	if err != nil || !ok || n.ID != 10 || n.Size != 5 {
		t.Fatalf("NodeByPath=(%+v,%v,%v)", n, ok, err)
	}
	n, ok, err = idx.NodeByPath(ctx, "sub/b.txt")
	if err != nil || !ok || n.ID != 12 || n.Path != "/sub/b.txt" {
		t.Fatalf("NodeByPath nested=(%+v,%v,%v)", n, ok, err)
	}

	kids, err := idx.Children(ctx, 1)
	if err != nil {
		t.Fatalf("Children: %v", err)
	}
	if len(kids) != 2 || !kids[0].IsDir || kids[0].Name != "sub" || kids[1].Name != "a.txt" {
		t.Fatalf("Children=%+v", kids)
	}
}

// Test 改名只动一条，但子树路径整体跟随。
func TestNodesMoveSubtree(t *testing.T) {
	idx, ctx := newNodes(t)
	_ = idx.PutNode(ctx, Node{ID: 11, ParentID: 1, Name: "sub", IsDir: true})
	_ = idx.PutNode(ctx, Node{ID: 12, ParentID: 11, Name: "b.txt", Size: 7})
	_ = idx.PutNode(ctx, Node{ID: 13, ParentID: 12, Name: "c", IsDir: true})
	_ = idx.PutNode(ctx, Node{ID: 14, ParentID: 13, Name: "d.txt"})

	oldPath, newPath, err := idx.MoveNode(ctx, 11, 1, "sub2")
	if err != nil {
		t.Fatalf("MoveNode: %v", err)
	}
	if oldPath != "/sub" || newPath != "/sub2" {
		t.Fatalf("old=%q new=%q", oldPath, newPath)
	}
	for id, want := range map[int]string{11: "/sub2", 12: "/sub2/b.txt", 13: "/sub2/b.txt/c", 14: "/sub2/b.txt/c/d.txt"} {
		n, ok, err := idx.NodeByID(ctx, id)
		if err != nil || !ok || n.Path != want {
			t.Fatalf("node %d path=%q want %q (err=%v ok=%v)", id, n.Path, want, err, ok)
		}
	}

	if _, _, err := idx.MoveNode(ctx, 1, 11, "x"); err == nil {
		t.Fatal("把目录移进自己的子树应报错")
	}
	if _, _, err := idx.MoveNode(ctx, 12, 1, "sub2"); err == nil {
		t.Fatal("目标名字已被占用应报错")
	}
}

// Test 删除子树返回全部消息号，且不留续块登记。
func TestNodesDeleteSubtree(t *testing.T) {
	idx, ctx := newNodes(t)
	_ = idx.PutNode(ctx, Node{ID: 11, ParentID: 1, Name: "sub", IsDir: true})
	_ = idx.PutNode(ctx, Node{ID: 12, ParentID: 11, Name: "b.txt"})
	_ = idx.PutChunk(ctx, 11, 90)
	_ = idx.PutNode(ctx, Node{ID: 20, ParentID: 1, Name: "keep.txt"})

	ids, err := idx.DeleteSubtree(ctx, 11)
	if err != nil {
		t.Fatalf("DeleteSubtree: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("删了 %v，期望 2 个节点", ids)
	}
	if _, ok, _ := idx.NodeByID(ctx, 11); ok {
		t.Fatal("目录节点应已删除")
	}
	if _, ok, _ := idx.ChunkOwner(ctx, 90); ok {
		t.Fatal("续块登记应已清除")
	}
	if _, ok, _ := idx.NodeByID(ctx, 20); !ok {
		t.Fatal("兄弟节点不应被删")
	}
}

// Test 续块登记与清理。
func TestNodesChunks(t *testing.T) {
	idx, ctx := newNodes(t)
	if err := idx.PutChunk(ctx, 5, 60); err != nil {
		t.Fatalf("PutChunk: %v", err)
	}
	if err := idx.PutChunk(ctx, 5, 61); err != nil {
		t.Fatalf("PutChunk: %v", err)
	}
	if dir, ok, err := idx.ChunkOwner(ctx, 61); err != nil || !ok || dir != 5 {
		t.Fatalf("ChunkOwner=(%d,%v,%v)", dir, ok, err)
	}
	drop, err := idx.DeleteChunks(ctx, 5, map[int]bool{60: true})
	if err != nil {
		t.Fatalf("DeleteChunks: %v", err)
	}
	if len(drop) != 1 || drop[0] != 61 {
		t.Fatalf("DeleteChunks=%v", drop)
	}
	if _, ok, _ := idx.ChunkOwner(ctx, 60); !ok {
		t.Fatal("保留的续块不应被删")
	}
}

// Test 文件节点查询与路径前缀边界。
func TestNodesFileNodes(t *testing.T) {
	idx, ctx := newNodes(t)
	_ = idx.PutNode(ctx, Node{ID: 11, ParentID: 1, Name: "sub", IsDir: true})
	_ = idx.PutNode(ctx, Node{ID: 12, ParentID: 1, Name: "a.txt", Size: 1})
	_ = idx.PutNode(ctx, Node{ID: 13, ParentID: 11, Name: "b.txt", Size: 2})
	_ = idx.PutNode(ctx, Node{ID: 14, ParentID: 1, Name: "subway.txt", Size: 3})

	files, err := idx.FileNodes(ctx, "/sub")
	if err != nil {
		t.Fatalf("FileNodes: %v", err)
	}
	if len(files) != 1 || files[0].ID != 13 {
		t.Fatalf("FileNodes=%+v（/subway.txt 不应算进 /sub）", files)
	}

	all, err := idx.FileNodes(ctx, "/")
	if err != nil {
		t.Fatalf("FileNodes root: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("根下应有 3 个文件，实际 %d", len(all))
	}
}
