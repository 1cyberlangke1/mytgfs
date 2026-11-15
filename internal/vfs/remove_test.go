package vfs

// 删除测试：删目录只给目录自己的消息打删除标记，子节点消息不动，
// 本地索引整棵子树一起清。

import (
	"errors"
	"os"
	"testing"

	"mytgfs/internal/dirent"
)

// TestRemoveAllDirectoryTombstonesSubtreeRoot 删目录时只有目录消息被打标记，
// 子节点消息远端不动，本地索引整棵子树清空。
func TestRemoveAllDirectoryTombstonesSubtreeRoot(t *testing.T) {
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
	node, ok := dirent.Parse(st.edits[dirID])
	if !ok || !node.Tombstone {
		t.Fatalf("目录消息应被打删除标记，实际 %q", st.edits[dirID])
	}
	// 子节点消息只保留原文，不被编辑也不被删
	for _, id := range append([]int{subID}, fileIDs...) {
		if _, edited := st.edits[id]; edited {
			t.Fatalf("子节点消息 %d 不应被编辑", id)
		}
		if _, exists := st.msgs[id]; !exists {
			t.Fatalf("子节点消息 %d 应在远端保留", id)
		}
	}
	for _, p := range []string{"/d", "/d/f1.bin", "/d/sub", "/d/sub/f2.bin"} {
		if _, ok, err := fsys.idx.NodeByPath(ctx, p); err != nil || ok {
			t.Fatalf("%s 索引应删除: ok=%v err=%v", p, ok, err)
		}
	}
}

// TestRemoveAllFileTombstonesOwnMessage 单文件删除只标记自己的消息，
// 同目录其它文件不受影响。
func TestRemoveAllFileTombstonesOwnMessage(t *testing.T) {
	fsys, st, done := newTestFS(t)
	defer done()
	ctx := t.Context()
	putRenameFixture(t, fsys)

	id := nodeID(t, fsys, "/d/f1.bin")
	if err := fsys.RemoveAll(ctx, "/d/f1.bin"); err != nil {
		t.Fatalf("删文件应成功: %v", err)
	}
	node, ok := dirent.Parse(st.edits[id])
	if !ok || !node.Tombstone {
		t.Fatalf("文件消息应被打删除标记，实际 %q", st.edits[id])
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
	if len(st.edits) != 0 {
		t.Fatalf("被拒绝时不应编辑任何消息，实际 %v", st.edits)
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
