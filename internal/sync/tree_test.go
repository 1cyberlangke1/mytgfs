package sync

// 全量重建与节点刷新单测：父指针、父缺失挂根、成环防死循环、改名/移动刷新。

import (
	"context"
	"testing"

	"mytgfs/internal/index"
	"mytgfs/internal/telegramstore"
)

func testNodes() []telegramstore.NodeInfo {
	return []telegramstore.NodeInfo{
		{MessageID: 1, Name: "dir", Parent: 0, IsDir: true},
		{MessageID: 2, Name: "a.txt", Parent: 1, Size: 3, Sha: "aa"},
		{MessageID: 3, Name: "sub", Parent: 1, IsDir: true},
		{MessageID: 4, Name: "b.txt", Parent: 3, Size: 4, Sha: "bb"},
	}
}

// TestLayoutBuildsTree 父子关系拼出完整路径。
func TestLayoutBuildsTree(t *testing.T) {
	layout, err := Layout(testNodes())
	if err != nil {
		t.Fatalf("Layout: %v", err)
	}
	want := map[int]string{1: "/dir", 2: "/dir/a.txt", 3: "/dir/sub", 4: "/dir/sub/b.txt"}
	for id, p := range want {
		if layout[id].Path != p {
			t.Fatalf("id=%d path=%q want %q", id, layout[id].Path, p)
		}
	}
}

// TestLayoutOrphanAtRoot 父消息号指向不存在的节点时挂到卷根。
func TestLayoutOrphanAtRoot(t *testing.T) {
	nodes := []telegramstore.NodeInfo{
		{MessageID: 9, Name: "orphan.txt", Parent: 999, Sha: "x"},
	}
	layout, err := Layout(nodes)
	if err != nil {
		t.Fatalf("Layout: %v", err)
	}
	if layout[9].Path != "/orphan.txt" || layout[9].ParentID != 0 {
		t.Fatalf("孤儿节点=%+v", layout[9])
	}
}

// TestLayoutBreaksCycle 父子成环时环上节点的 parent 归零，本地不成环。
func TestLayoutBreaksCycle(t *testing.T) {
	nodes := []telegramstore.NodeInfo{
		{MessageID: 1, Name: "a", Parent: 2, IsDir: true},
		{MessageID: 2, Name: "b", Parent: 1, IsDir: true},
	}
	layout, err := Layout(nodes)
	if err != nil {
		t.Fatalf("Layout: %v", err)
	}
	for id, n := range layout {
		if n.ParentID != 0 {
			t.Fatalf("成环节点 %d 的 parent 应归零: %+v", id, n)
		}
	}
	ctx := t.Context()
	idx := newTestIndex(t)
	if _, err := Rebuild(ctx, idx, nodes); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	// 沿 parent 链上溯必须终止，不能无限回绕
	cur := 1
	for range 8 {
		n, ok, _ := idx.NodeByID(ctx, cur)
		if !ok {
			t.Fatalf("节点 %d 未写入", cur)
		}
		if n.ParentID == 0 {
			return
		}
		cur = n.ParentID
	}
	t.Fatal("本地 parent 链成环")
}

// TestLayoutLargerIDWinsOnSameName 同名冲突按消息号大到小保留一条。
func TestLayoutLargerIDWinsOnSameName(t *testing.T) {
	nodes := []telegramstore.NodeInfo{
		{MessageID: 2, Name: "a.txt", Sha: "new"},
		{MessageID: 1, Name: "a.txt", Sha: "old"},
	}
	layout, err := Layout(nodes)
	if err != nil {
		t.Fatalf("Layout: %v", err)
	}
	if len(layout) != 1 || layout[2].Sha256 != "new" {
		t.Fatalf("同名冲突结果=%+v", layout)
	}
}

// TestRebuildWritesIndex 全量重建清空旧索引后写入全部节点。
func TestRebuildWritesIndex(t *testing.T) {
	ctx := t.Context()
	idx := newTestIndex(t)
	_ = idx.PutNode(ctx, index.Node{ID: 99, Name: "stale"})
	n, err := Rebuild(ctx, idx, testNodes())
	if err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	if n != 2 {
		t.Fatalf("文件数=%d", n)
	}
	if _, ok, _ := idx.NodeByID(ctx, 99); ok {
		t.Fatal("旧节点应被清掉")
	}
	if _, ok, _ := idx.NodeByPath(ctx, "/dir/sub/b.txt"); !ok {
		t.Fatal("深层路径应重建出来")
	}
}

// TestRefreshNodeNewInsertsWithParent 全新节点补齐父链后插入。
func TestRefreshNodeNewInsertsWithParent(t *testing.T) {
	ctx := t.Context()
	idx := newTestIndex(t)
	nodes := newFakeNodes()
	nodes.put(1, "dir", 0, true, 0, "")
	nodes.put(2, "a.txt", 1, false, 3, "aa")

	p, ok, err := RefreshNode(ctx, idx, nodes, 2)
	if err != nil || !ok || p != "/dir/a.txt" {
		t.Fatalf("RefreshNode=(%q,%v,%v)", p, ok, err)
	}
	if _, exists, _ := idx.NodeByID(ctx, 1); !exists {
		t.Fatal("父目录应被补齐")
	}
}

// TestRefreshNodeRenameMovesSubtree 改名时本地整棵子树跟着改。
func TestRefreshNodeRenameMovesSubtree(t *testing.T) {
	ctx := t.Context()
	idx := newTestIndex(t)
	_, _ = Rebuild(ctx, idx, testNodes())
	nodes := newFakeNodes()
	for _, n := range testNodes() {
		nodes.nodes[n.MessageID] = n
	}
	nodes.nodes[1] = telegramstore.NodeInfo{MessageID: 1, Name: "renamed", Parent: 0, IsDir: true}

	if _, _, err := RefreshNode(ctx, idx, nodes, 1); err != nil {
		t.Fatalf("RefreshNode: %v", err)
	}
	if _, ok, _ := idx.NodeByPath(ctx, "/renamed/sub/b.txt"); !ok {
		t.Fatal("子树路径应跟着改")
	}
}

// TestRefreshNodeDeletedRemovesSubtree 远端消息没了，本地连子树一起删。
func TestRefreshNodeDeletedRemovesSubtree(t *testing.T) {
	ctx := t.Context()
	idx := newTestIndex(t)
	_, _ = Rebuild(ctx, idx, testNodes())
	p, ok, err := RefreshNode(ctx, idx, newFakeNodes(), 1)
	if err != nil || ok || p != "/dir" {
		t.Fatalf("RefreshNode=(%q,%v,%v)", p, ok, err)
	}
	if _, exists, _ := idx.NodeByID(ctx, 4); exists {
		t.Fatal("后代应被删")
	}
}

// TestDeleteRemoteKnownOnly 只处理本地认识的消息号。
func TestDeleteRemoteKnownOnly(t *testing.T) {
	ctx := context.Background()
	idx := newTestIndex(t)
	_, _ = Rebuild(ctx, idx, testNodes())
	if _, handled, _ := DeleteRemote(ctx, idx, 999); handled {
		t.Fatal("未知消息不该算处理过")
	}
	removed, handled, err := DeleteRemote(ctx, idx, 1)
	if err != nil || !handled || len(removed) != 4 {
		t.Fatalf("DeleteRemote=(%v,%v,%v)", removed, handled, err)
	}
}
