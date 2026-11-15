package vfs

// 跨卷复制单测：不重传字节、幂等、目标冲突与强制覆盖。

import (
	"errors"
	"testing"

	"mytgfs/internal/push"
	"mytgfs/internal/queue"
)

// wireCopySource 让 dst 把 "src" 卷解析成本测试里的源文件系统。
//
// 输入：dst 目标卷；name 源卷名；src 源卷
// 输出：无
// 预期：源卷名不匹配时返回错误，用来顺带验证解析器真的被调用
func wireCopySource(dst *FileSystem, name string, src *FileSystem, store *fakeStore) {
	dst.SetCopySource(func(vol string) (Peer, error) {
		if vol != name {
			return Peer{}, errors.New("unknown volume " + vol)
		}
		return Peer{Index: src.idx, Store: store}, nil
	})
}

// TestCopyAcrossVolumes 跨卷复制只发一条新消息，不重传字节。
func TestCopyAcrossVolumes(t *testing.T) {
	ctx := t.Context()
	srcFS, srcStore, _, srcCleanup := newTestFSWithEvents(t)
	defer srcCleanup()
	dstFS, dstStore, rec, dstCleanup := newTestFSWithEvents(t)
	defer dstCleanup()

	putFile(t, srcFS, "/a.txt", "hello")
	src, ok, err := srcFS.idx.NodeByPath(ctx, "/a.txt")
	if err != nil || !ok {
		t.Fatalf("源节点未登记: ok=%v err=%v", ok, err)
	}
	wireCopySource(dstFS, "src", srcFS, srcStore)

	rec.Reset()
	if err := dstFS.Copy(ctx, "src", "/a.txt", "/b.txt", false); err != nil {
		t.Fatalf("Copy: %v", err)
	}

	got, ok, err := dstFS.idx.NodeByPath(ctx, "/b.txt")
	if err != nil || !ok {
		t.Fatalf("目标节点未登记: ok=%v err=%v", ok, err)
	}
	if got.Sha256 != src.Sha256 || got.Size != src.Size {
		t.Fatalf("目标内容不一致: %+v vs %+v", got, src)
	}
	if len(dstStore.uploadKeys) != 0 {
		t.Fatalf("复制不该重传，uploadKeys=%v", dstStore.uploadKeys)
	}
	if len(dstStore.copiedSrc) != 1 || dstStore.copiedSrc[0] != src.DocumentID {
		t.Fatalf("copiedSrc=%v，期望引用源 document", dstStore.copiedSrc)
	}
	if rec.Count(push.TopicStatus) == 0 {
		t.Fatal("复制完成没有推送 status")
	}
}

// TestCopyIdempotent 重复执行同一次复制不再发消息。
func TestCopyIdempotent(t *testing.T) {
	ctx := t.Context()
	srcFS, srcStore, _, srcCleanup := newTestFSWithEvents(t)
	defer srcCleanup()
	dstFS, dstStore, _, dstCleanup := newTestFSWithEvents(t)
	defer dstCleanup()

	putFile(t, srcFS, "/a.txt", "hello")
	wireCopySource(dstFS, "src", srcFS, srcStore)

	for i := 0; i < 2; i++ {
		if err := dstFS.Copy(ctx, "src", "/a.txt", "/b.txt", false); err != nil {
			t.Fatalf("第 %d 次 Copy: %v", i+1, err)
		}
	}
	if len(dstStore.copiedSrc) != 1 {
		t.Fatalf("重放重复发了消息: %v", dstStore.copiedSrc)
	}
}

// TestCopyRejectsExisting 目标已有不同内容且未加 --force 时永久失败。
func TestCopyRejectsExisting(t *testing.T) {
	ctx := t.Context()
	srcFS, srcStore, _, srcCleanup := newTestFSWithEvents(t)
	defer srcCleanup()
	dstFS, _, _, dstCleanup := newTestFSWithEvents(t)
	defer dstCleanup()

	putFile(t, srcFS, "/a.txt", "hello")
	putFile(t, dstFS, "/b.txt", "other")
	wireCopySource(dstFS, "src", srcFS, srcStore)

	err := dstFS.Copy(ctx, "src", "/a.txt", "/b.txt", false)
	if err == nil {
		t.Fatal("目标已存在却复制成功")
	}
	if queue.Retryable(err) {
		t.Fatalf("目标冲突不该重试: %v", err)
	}
}

// TestCopyForceOverwrites 加 --force 时旧节点被打删除标记，只保留新内容。
func TestCopyForceOverwrites(t *testing.T) {
	ctx := t.Context()
	srcFS, srcStore, _, srcCleanup := newTestFSWithEvents(t)
	defer srcCleanup()
	dstFS, dstStore, _, dstCleanup := newTestFSWithEvents(t)
	defer dstCleanup()

	putFile(t, srcFS, "/a.txt", "hello")
	oldID := putFile(t, dstFS, "/b.txt", "other")
	wireCopySource(dstFS, "src", srcFS, srcStore)

	if err := dstFS.Copy(ctx, "src", "/a.txt", "/b.txt", true); err != nil {
		t.Fatalf("Copy --force: %v", err)
	}
	if got := tombstoneCount(dstStore); got != 1 {
		t.Fatalf("旧节点标记数=%d，期望 1", got)
	}
	got, ok, err := dstFS.idx.NodeByPath(ctx, "/b.txt")
	if err != nil || !ok {
		t.Fatalf("目标节点未登记: ok=%v err=%v", ok, err)
	}
	if got.ID == oldID {
		t.Fatal("覆盖后仍是旧消息号")
	}
}

// TestCopyMissingSource 源文件不在索引里时永久失败，不无限重试。
func TestCopyMissingSource(t *testing.T) {
	ctx := t.Context()
	srcFS, srcStore, _, srcCleanup := newTestFSWithEvents(t)
	defer srcCleanup()
	dstFS, _, _, dstCleanup := newTestFSWithEvents(t)
	defer dstCleanup()

	wireCopySource(dstFS, "src", srcFS, srcStore)

	err := dstFS.Copy(ctx, "src", "/gone.txt", "/b.txt", false)
	if err == nil {
		t.Fatal("源不存在却复制成功")
	}
	if queue.Retryable(err) {
		t.Fatalf("源不存在不该重试: %v", err)
	}
}

// TestCopyWithoutSource 没接入源卷解析时直接报错，不入队。
func TestCopyWithoutSource(t *testing.T) {
	dstFS, _, _, cleanup := newTestFSWithEvents(t)
	defer cleanup()

	if err := dstFS.Copy(t.Context(), "src", "/a.txt", "/b.txt", false); err == nil {
		t.Fatal("没有 copySource 却复制成功")
	}
}

// TestCopyGoesThroughQueue 复制落进持久队列，完成后任务能在快照里查到。
func TestCopyGoesThroughQueue(t *testing.T) {
	ctx := t.Context()
	srcFS, srcStore, _, srcCleanup := newTestFSWithEvents(t)
	defer srcCleanup()
	dstFS, _, _, dstCleanup := newTestFSWithEvents(t)
	defer dstCleanup()

	putFile(t, srcFS, "/a.txt", "hello")
	wireCopySource(dstFS, "src", srcFS, srcStore)

	// 队列按卷名串行，未设卷名的任务会被拒绝
	dstFS.SetVolume("dst")
	q, err := dstFS.AttachQueue(ctx)
	if err != nil {
		t.Fatalf("AttachQueue: %v", err)
	}
	defer dstFS.Close()

	if cerr := dstFS.Copy(ctx, "src", "/a.txt", "/b.txt", false); cerr != nil {
		t.Fatalf("Copy: %v", cerr)
	}
	var seen bool
	for _, it := range q.Snapshot() {
		if it.Kind == queue.KindCopy && it.State == queue.StateDone {
			seen = true
		}
	}
	if !seen {
		t.Fatalf("队列里没有完成的 copy 任务: %+v", q.Snapshot())
	}
}
