package vfs

// 删除测试：删目录按子树批量删消息，本地索引一起清。

import (
	"errors"
	"os"
	"slices"
	"testing"
)

// TestRemoveAllDirectoryRemovesSubtree 删目录时子树里每个节点的消息都进删除批次。
func TestRemoveAllDirectoryRemovesSubtree(t *testing.T) {
	fsys, st, done := newTestFS(t)
	defer done()
	ctx := t.Context()
	putRenameFixture(t, fsys)

	dirID := nodeID(t, fsys, "/d")
	subID := nodeID(t, fsys, "/d/sub")
	fileIDs := []int{nodeID(t, fsys, "/d/f1.bin"), nodeID(t, fsys, "/d/sub/f2.bin")}

	if err := fsys.RemoveAll(ctx, "/d"); err != nil {
		t.Fatalf("删目录应成功: %v", err)
	}
	for _, id := range append([]int{dirID, subID}, fileIDs...) {
		if !slices.Contains(st.deleted, id) {
			t.Fatalf("消息 %d 应被删除，实际 %v", id, st.deleted)
		}
		if _, exists := st.msgs[id]; exists {
			t.Fatalf("远端消息 %d 应被删", id)
		}
	}
	for _, p := range []string{"/d", "/d/f1.bin", "/d/sub", "/d/sub/f2.bin"} {
		if _, ok, err := fsys.idx.NodeByPath(ctx, p); err != nil || ok {
			t.Fatalf("%s 索引应删除: ok=%v err=%v", p, ok, err)
		}
	}
}

// TestRemoveAllFileDeletesMessage 单文件删除只删自己的消息，同目录其它文件不受影响。
func TestRemoveAllFileDeletesMessage(t *testing.T) {
	fsys, st, done := newTestFS(t)
	defer done()
	ctx := t.Context()
	putRenameFixture(t, fsys)

	id := nodeID(t, fsys, "/d/f1.bin")
	if err := fsys.RemoveAll(ctx, "/d/f1.bin"); err != nil {
		t.Fatalf("删文件应成功: %v", err)
	}
	if len(st.deleted) != 1 || st.deleted[0] != id {
		t.Fatalf("应只删消息 %d，实际 %v", id, st.deleted)
	}
	if _, ok, _ := fsys.idx.NodeByPath(ctx, "/d/sub/f2.bin"); !ok {
		t.Fatal("同目录其它文件不应受影响")
	}
	children, _ := fsys.idx.Children(ctx, nodeID(t, fsys, "/d"))
	if len(children) != 1 || children[0].Name != "sub" {
		t.Fatalf("目录应只剩 sub: %+v", children)
	}
}

// TestRemoveAllKeepsRoot 虚拟卷根仍拒绝删除。
func TestRemoveAllKeepsRoot(t *testing.T) {
	fsys, st, done := newTestFS(t)
	defer done()
	ctx := t.Context()
	putFile(t, fsys, "/x.bin", "x")

	if err := fsys.RemoveAll(ctx, "/"); err == nil {
		t.Fatal("根目录删除应报错")
	}
	if len(st.deleted) != 0 {
		t.Fatalf("被拒绝时不应删任何消息，实际 %v", st.deleted)
	}
	if _, ok, _ := fsys.idx.NodeByPath(ctx, "/x.bin"); !ok {
		t.Fatal("被拒绝时索引不应变动")
	}
}

// TestRemoveAllMissingPath 不存在的路径应表现为 ErrNotExist，而不是静默成功。
func TestRemoveAllMissingPath(t *testing.T) {
	fsys, _, done := newTestFS(t)
	defer done()
	err := fsys.RemoveAll(t.Context(), "/nope.bin")
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("不存在应返回 ErrNotExist，实际 %v", err)
	}
}
