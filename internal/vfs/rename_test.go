package vfs

// 改名测试：只编辑节点自己那条消息，子树里的文件消息一条都不动。

import (
	"errors"
	"os"
	"testing"

	"mytgfs/internal/dirent"
)

// TestRenameDirectoryOnlyEditsOwnMessage 目录改名后节点正文换名，子树消息号不变。
func TestRenameDirectoryOnlyEditsOwnMessage(t *testing.T) {
	fsys, st, done := newTestFS(t)
	defer done()
	ctx := t.Context()
	putRenameFixture(t, fsys)
	uploads, copies, deletes := len(st.uploadKeys), len(st.copiedSrc), len(st.deleted)
	dirID := nodeID(t, fsys, "/d")

	before := map[string]int{}
	for _, p := range []string{"/d/f1.bin", "/d/sub/f2.bin"} {
		n, ok, _ := fsys.idx.NodeByPath(ctx, p)
		if !ok {
			t.Fatalf("%s 预置失败", p)
		}
		before[p] = n.ID
	}

	if err := fsys.Rename(ctx, "/d", "/e"); err != nil {
		t.Fatalf("目录改名应成功: %v", err)
	}

	if len(st.uploadKeys) != uploads || len(st.copiedSrc) != copies || len(st.deleted) != deletes {
		t.Fatalf("改名不应产生上传/引用/删除: keys=%v copy=%v del=%v", st.uploadKeys, st.copiedSrc, st.deleted)
	}
	// 只编辑了目录自己那条消息
	if len(st.edits) != 1 {
		t.Fatalf("应只编辑 1 条消息，实际 %v", st.edits)
	}
	edited, exists := st.edits[dirID]
	if !exists {
		t.Fatalf("应编辑目录消息 %d，实际 %v", dirID, st.edits)
	}
	node, ok := dirent.Parse(edited)
	if !ok || node.Name != "e" || !node.Dir || node.Parent != 0 {
		t.Fatalf("改名后的正文不符: %+v (%s)", node, edited)
	}
	for _, p := range []string{"/e/f1.bin", "/e/sub/f2.bin"} {
		n, ok, _ := fsys.idx.NodeByPath(ctx, p)
		if !ok {
			t.Fatalf("改名后应存在 %s", p)
		}
		old := "/d/" + p[3:]
		if want := before[old]; want != 0 && n.ID != want {
			t.Fatalf("%s 消息号应保持 %d，实际 %d", p, want, n.ID)
		}
		// 子节点的正文没被编辑（名字/父号仍是原值）
		if _, touched := st.edits[n.ID]; touched {
			t.Fatalf("子节点 %d 不该被编辑", n.ID)
		}
	}
	for _, p := range []string{"/d", "/d/f1.bin", "/d/sub/f2.bin"} {
		if _, ok, _ := fsys.idx.NodeByPath(ctx, p); ok {
			t.Fatalf("旧路径应消失: %s", p)
		}
	}
}

// TestRenameEmptyDirectory 空目录同样只改自己那条消息。
func TestRenameEmptyDirectory(t *testing.T) {
	fsys, st, done := newTestFS(t)
	defer done()
	ctx := t.Context()
	id := mkdir(t, fsys, "/empty")

	if err := fsys.Rename(ctx, "/empty", "/empty2"); err != nil {
		t.Fatalf("空目录改名应成功: %v", err)
	}
	if len(st.edits) != 1 {
		t.Fatalf("应只编辑 1 条消息，实际 %v", st.edits)
	}
	node, ok := dirent.Parse(st.edits[id])
	if !ok || node.Name != "empty2" || !node.Dir {
		t.Fatalf("目录正文应改名: %+v", node)
	}
	if _, ok, _ := fsys.idx.NodeByPath(ctx, "/empty2"); !ok {
		t.Fatal("新目录应存在")
	}
	if _, ok, _ := fsys.idx.NodeByPath(ctx, "/empty"); ok {
		t.Fatal("旧目录应消失")
	}
}

// TestRenameMovesAcrossParents 跨父移动只编辑节点自己一条消息并改 parent。
func TestRenameMovesAcrossParents(t *testing.T) {
	fsys, st, done := newTestFS(t)
	defer done()
	ctx := t.Context()
	putRenameFixture(t, fsys)
	mkdir(t, fsys, "/target")
	targetID := nodeID(t, fsys, "/target")
	fileID := nodeID(t, fsys, "/d/f1.bin")

	if err := fsys.Rename(ctx, "/d/f1.bin", "/target/moved.bin"); err != nil {
		t.Fatalf("跨父移动应成功: %v", err)
	}
	if len(st.edits) != 1 {
		t.Fatalf("应只编辑 1 条消息，实际 %v", st.edits)
	}
	node, ok := dirent.Parse(st.edits[fileID])
	if !ok || node.Name != "moved.bin" || node.Parent != targetID {
		t.Fatalf("移动后的正文不符: %+v", node)
	}
	n, ok, _ := fsys.idx.NodeByPath(ctx, "/target/moved.bin")
	if !ok || n.ID != fileID || n.ParentID != targetID {
		t.Fatalf("索引不符: %+v", n)
	}
}

// TestRenameDirectoryRejectsOwnSubtree 移进自己的子树会形成环，必须拒绝。
func TestRenameDirectoryRejectsOwnSubtree(t *testing.T) {
	fsys, st, done := newTestFS(t)
	defer done()
	ctx := t.Context()
	putRenameFixture(t, fsys)

	if err := fsys.Rename(ctx, "/d", "/d/sub/d2"); err == nil {
		t.Fatal("移进自身子树应报错")
	}
	if len(st.edits) != 0 {
		t.Fatalf("被拒绝时不该编辑消息: %v", st.edits)
	}
	if _, ok, _ := fsys.idx.NodeByPath(ctx, "/d/f1.bin"); !ok {
		t.Fatal("被拒绝时索引不应变动")
	}
}

// TestRenameDirectoryRejectsExistingTarget 目标已存在时拒绝，避免覆盖。
func TestRenameDirectoryRejectsExistingTarget(t *testing.T) {
	fsys, _, done := newTestFS(t)
	defer done()
	ctx := t.Context()
	putRenameFixture(t, fsys)
	mkdir(t, fsys, "/other")

	err := fsys.Rename(ctx, "/d", "/other")
	if !errors.Is(err, os.ErrExist) {
		t.Fatalf("目标存在时应返回 ErrExist，实际 %v", err)
	}
	if _, ok, _ := fsys.idx.NodeByPath(ctx, "/d/f1.bin"); !ok {
		t.Fatal("被拒绝时索引不应变动")
	}
}

// TestRenameRootRejected 虚拟卷根不能改名。
func TestRenameRootRejected(t *testing.T) {
	fsys, _, done := newTestFS(t)
	defer done()
	if err := fsys.Rename(t.Context(), "/", "/x"); err == nil {
		t.Fatal("根目录改名应报错")
	}
}

// TestRenameEditFailureLeavesLocalUntouched 远端编辑失败时本地节点与正文都不变。
func TestRenameEditFailureLeavesLocalUntouched(t *testing.T) {
	fsys, st, done := newTestFS(t)
	defer done()
	ctx := t.Context()
	putRenameFixture(t, fsys)
	st.editErr = errors.New("network down")
	id := nodeID(t, fsys, "/d")

	if err := fsys.Rename(ctx, "/d", "/e"); err == nil {
		t.Fatal("远端编辑失败应返回错误")
	}
	if _, ok, _ := fsys.idx.NodeByPath(ctx, "/d"); !ok {
		t.Fatal("本地旧路径不应消失")
	}
	if _, ok, _ := fsys.idx.NodeByPath(ctx, "/e"); ok {
		t.Fatal("本地不该出现新路径")
	}
	node, _ := dirent.Parse(st.msgs[id].body)
	if node.Name != "d" {
		t.Fatalf("远端正文不该被改: %+v", node)
	}
}

// TestReconcileNodeFromRemote 本地与远端分叉时按远端正文纠正回来。
func TestReconcileNodeFromRemote(t *testing.T) {
	fsys, st, done := newTestFS(t)
	defer done()
	ctx := t.Context()
	putRenameFixture(t, fsys)
	id := nodeID(t, fsys, "/d")

	// 远端已改名（模拟 EditNode 成功、本地 MoveNode 失败后的状态）
	newBody, _ := dirent.Encode(dirent.Node{Name: "remote-name", Parent: 0, Dir: true})
	st.msgs[id] = nodeMsg{body: newBody}

	if err := fsys.reconcileNode(ctx, id); err != nil {
		t.Fatalf("reconcileNode: %v", err)
	}
	n, ok, _ := fsys.idx.NodeByPath(ctx, "/remote-name")
	if !ok || n.ID != id {
		t.Fatalf("应按远端名字恢复: %+v ok=%v", n, ok)
	}
	if _, ok, _ := fsys.idx.NodeByPath(ctx, "/d"); ok {
		t.Fatal("旧名字应消失")
	}
}

// TestReconcileNodeDeletesMissingRemote 远端消息已删时本地节点被清掉。
func TestReconcileNodeDeletesMissingRemote(t *testing.T) {
	fsys, st, done := newTestFS(t)
	defer done()
	ctx := t.Context()
	putRenameFixture(t, fsys)
	id := nodeID(t, fsys, "/d/f1.bin")
	delete(st.msgs, id)

	if err := fsys.reconcileNode(ctx, id); err != nil {
		t.Fatalf("reconcileNode: %v", err)
	}
	if _, ok, _ := fsys.idx.NodeByID(ctx, id); ok {
		t.Fatal("远端没有的消息不该留在本地")
	}
}
