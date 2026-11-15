package vfs

// 上传登记与临时文件账本的回归测试：
// 目标是「远端正文字段」与「本地节点」永远指向同一个目录，
// 任何一步失败都必须明确报错，不能静默丢文件。

import (
	"errors"
	"os"
	"testing"

	"mytgfs/internal/dirent"
	"mytgfs/internal/index"
	"mytgfs/internal/queue"
	"mytgfs/internal/telegramstore"
)

// 父目录在上传排队期间改名：登记用消息里定稿的父号，不能按旧路径重查。
func TestRecordUsesJobParentAfterParentRename(t *testing.T) {
	fsys, st, done := newTestFS(t)
	defer done()
	ctx := t.Context()

	parent := mkdir(t, fsys, "/a")
	if err := fsys.Rename(ctx, "/a", "/b"); err != nil {
		t.Fatalf("改名父目录: %v", err)
	}
	// 消息正文里的父号是改名前定稿的，登记也必须用同一个
	part := &telegramstore.Part{MessageID: 900, DocumentID: 1, AccessHash: 2, Size: 3}
	f := &file{d: fsys, name: "/a/x.bin"}
	if err := f.record(ctx, "/a/x.bin", parent, "x.bin", "deadbeef", part); err != nil {
		t.Fatalf("record: %v", err)
	}

	n, ok, err := fsys.idx.NodeByID(ctx, 900)
	if err != nil || !ok {
		t.Fatalf("节点未登记: ok=%v err=%v", ok, err)
	}
	if n.ParentID != parent {
		t.Fatalf("父号 = %d, 期望 %d（必须与消息正文一致）", n.ParentID, parent)
	}
	if n.Name != "x.bin" || n.Path != "/b/x.bin" {
		t.Fatalf("节点 = %+v, 期望 /b/x.bin", n)
	}
	if _, exists, _ := fsys.idx.NodeByPath(ctx, "/a/x.bin"); exists {
		t.Fatal("不该按旧路径另建一个节点")
	}
	if len(st.uploadKeys) != 0 {
		t.Fatal("record 不该再触发上传")
	}
}

// 目标目录在排队期间被删：消息已发出但没有归属，必须打标记并明确失败。
func TestRecordRejectsMissingTargetAndMarksMessage(t *testing.T) {
	fsys, st, done := newTestFS(t)
	defer done()
	ctx := t.Context()

	parent := mkdir(t, fsys, "/a")
	if err := fsys.RemoveAll(ctx, "/a"); err != nil {
		t.Fatalf("删目录: %v", err)
	}
	part := &telegramstore.Part{MessageID: 901, Size: 1}
	f := &file{d: fsys, name: "/a/x.bin"}
	err := f.record(ctx, "/a/x.bin", parent, "x.bin", "sha", part)
	if err == nil {
		t.Fatal("目标目录已删时必须报错，不能静默成功")
	}
	if queue.Retryable(err) {
		t.Fatalf("确定性失败不该重试: %v", err)
	}
	if _, ok, _ := fsys.idx.NodeByID(ctx, 901); ok {
		t.Fatal("不能留下父节点不存在的本地孤儿")
	}
	node, ok := dirent.Parse(st.edits[901])
	if !ok || !node.Tombstone {
		t.Fatalf("无主消息应打删除标记, 实际正文 = %q", st.edits[901])
	}
}

// 空文件不能假装上传成功。
func TestEmptyUploadRejected(t *testing.T) {
	fsys, st, done := newTestFS(t)
	defer done()
	ctx := t.Context()

	f, err := fsys.OpenFile(ctx, "/empty.bin", os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatalf("打开: %v", err)
	}
	cerr := f.Close()
	if cerr == nil {
		t.Fatal("0 字节上传应报错")
	}
	if len(st.uploadKeys) != 0 {
		t.Fatal("空文件不该发消息")
	}
	if _, ok, _ := fsys.idx.NodeByPath(ctx, "/empty.bin"); ok {
		t.Fatal("空文件不该登记节点")
	}
	if pending, _ := fsys.idx.ListPending(ctx); len(pending) != 0 {
		t.Fatalf("空文件不该留 pending 账本: %+v", pending)
	}
}

// 临时文件与记账尺寸不符：清账并明确失败，不静默丢文件。
func TestUploadNowRejectsTruncatedTemp(t *testing.T) {
	fsys, st, done := newTestFS(t)
	defer done()
	ctx := t.Context()

	tmp, err := os.CreateTemp(t.TempDir(), "up-*")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tmp.Write([]byte("abc")); err != nil {
		t.Fatal(err)
	}
	if err := tmp.Close(); err != nil {
		t.Fatal(err)
	}
	if err := fsys.idx.PutPending(ctx, index.PendingUpload{
		Path: "/x.bin", Temp: tmp.Name(), Size: 10,
	}); err != nil {
		t.Fatal(err)
	}

	err = fsys.uploadNow(ctx, uploadJob{
		Path: "/x.bin", Temp: tmp.Name(), Size: 10, Sha: "s", Base: "x.bin", Parent: 0,
	}, nil)
	if err == nil {
		t.Fatal("尺寸不符应报错")
	}
	if queue.Retryable(err) {
		t.Fatalf("尺寸不符重放也不会好，不该重试: %v", err)
	}
	if pending, _ := fsys.idx.ListPending(ctx); len(pending) != 0 {
		t.Fatalf("账本应清掉: %+v", pending)
	}
	if _, serr := os.Stat(tmp.Name()); !errors.Is(serr, os.ErrNotExist) {
		t.Fatal("坏临时文件应删除")
	}
	if len(st.uploadKeys) != 0 {
		t.Fatal("不该上传")
	}
}
