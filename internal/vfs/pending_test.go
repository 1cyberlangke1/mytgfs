package vfs

// 待补传记账单测：内容写完整后先在索引里记一笔，提交崩溃后能按账恢复。
// 边界：只处理本程序记下的 pending 记录，绝不扫描或删除频道里任何别的消息。

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"mytgfs/internal/index"
	"mytgfs/internal/queue"
)

const helloSHA = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"

// TestFailedUploadKeepsPendingAndTemp 上传失败时保留账本与临时文件，供重启补传。
func TestFailedUploadKeepsPendingAndTemp(t *testing.T) {
	fsys, st, done := newTestFS(t)
	defer done()
	st.uploadErr = errors.New("network down")

	f, err := fsys.OpenFile(context.Background(), "/a.bin", os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err == nil {
		t.Fatal("上传失败时 Close 应返回错误")
	}

	rows, err := fsys.idx.ListPending(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Path != "/a.bin" || rows[0].Size != 5 {
		t.Fatalf("失败应保留一条 pending，实际 %+v", rows)
	}
	if _, err := os.Stat(rows[0].Temp); err != nil {
		t.Fatalf("失败应保留临时文件: %v", err)
	}
}

// TestSuccessfulUploadClearsPending 上传成功后账本清空、临时文件删除。
func TestSuccessfulUploadClearsPending(t *testing.T) {
	fsys, _, done := newTestFS(t)
	defer done()

	f, err := fsys.OpenFile(context.Background(), "/a.bin", os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.Write([]byte("hello"))
	name := f.(*file).tmp.Name()
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	rows, _ := fsys.idx.ListPending(context.Background())
	if len(rows) != 0 {
		t.Fatalf("成功后 pending 应为空，实际 %+v", rows)
	}
	if _, err := os.Stat(name); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("成功后临时文件应删除: %v", err)
	}
}

// newResumeIndex 建一个空的临时索引；卷根是虚拟的，不需要根消息。
func newResumeIndex(t *testing.T) *index.Index {
	t.Helper()
	idx, err := index.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = idx.Close() })
	return idx
}

// attachQueue 给文件系统挂上持久化队列，测试结束关闭。
//
// 输入：t；fsys 待测文件系统
// 输出：已启动的队列，用于等待任务排空
// 预期：重启补传是异步的，测试必须等队列跑完再断言
func attachQueue(t *testing.T, fsys *FileSystem) *queue.Queue {
	t.Helper()
	// 队列按卷串行，任务必须带卷名；正式流程由 OpenVolume 设置
	fsys.SetVolume("resume-test")
	q, err := fsys.AttachQueue(t.Context())
	if err != nil {
		t.Fatalf("attach queue: %v", err)
	}
	t.Cleanup(fsys.Close)
	return q
}

// waitIdle 等待队列里没有排队或执行中的任务。
//
// 输入：t；q 队列
// 输出：无
// 预期：5 秒内排空，超时视为失败（不是靠固定 sleep 撞运气）
func waitIdle(t *testing.T, q *queue.Queue) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if q.Pending() == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("queue still has %d pending jobs", q.Pending())
}

// TestResumePendingUploadsAndIndexes 重启后按账本把未提交文件补传并登记索引。
func TestResumePendingUploadsAndIndexes(t *testing.T) {
	idx := newResumeIndex(t)
	ctx := context.Background()

	// 模拟上次崩溃：临时文件完整、账本有一笔，但没上传、没索引
	tmp := filepath.Join(t.TempDir(), ".mytgfs-dav-crash")
	if err := os.WriteFile(tmp, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := idx.PutPending(ctx, index.PendingUpload{Path: "/a.bin", Temp: tmp, Size: 5, Mtime: 1}); err != nil {
		t.Fatal(err)
	}

	st := newFakeStore()
	fsys := New(ctx, idx, st, 1<<20)
	q := attachQueue(t, fsys)
	if err := fsys.ResumePending(); err != nil {
		t.Fatal(err)
	}
	waitIdle(t, q)

	if len(st.uploadKeys) != 1 || st.uploadKeys[0] != helloSHA {
		t.Fatalf("应按内容 sha 补传，实际 %v", st.uploadKeys)
	}
	n, ok, err := idx.NodeByPath(ctx, "/a.bin")
	if err != nil || !ok || n.Size != 5 || n.Sha256 != helloSHA {
		t.Fatalf("补传后应登记索引: ok=%v err=%v node=%+v", ok, err, n)
	}
	if len(st.msgs) == 0 {
		t.Fatalf("补传后应产生一条节点消息")
	}
	rows, _ := idx.ListPending(ctx)
	if len(rows) != 0 {
		t.Fatalf("补传成功后账本应清空，实际 %+v", rows)
	}
	if _, err := os.Stat(tmp); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("补传成功后临时文件应删除: %v", err)
	}
}

// TestSuccessfulUploadClearsPendingDone 正常提交成功后不应残留已上传记账。
func TestSuccessfulUploadClearsPendingDone(t *testing.T) {
	fsys, _, done := newTestFS(t)
	defer done()
	ctx := context.Background()

	f, err := fsys.OpenFile(ctx, "/a.bin", os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := fsys.idx.GetPendingDone(ctx, "/a.bin"); err != nil || ok {
		t.Fatalf("成功后应清掉已上传记账: ok=%v err=%v", ok, err)
	}
}

// TestResumePendingReusesUploadedPart 上传已成功但索引没写成时，重启只补索引、不重传字节。
func TestResumePendingReusesUploadedPart(t *testing.T) {
	idx := newResumeIndex(t)
	ctx := context.Background()

	tmp := filepath.Join(t.TempDir(), ".mytgfs-dav-crash")
	if err := os.WriteFile(tmp, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := idx.PutPending(ctx, index.PendingUpload{Path: "/a.bin", Temp: tmp, Size: 5, Mtime: 1}); err != nil {
		t.Fatal(err)
	}
	if err := idx.PutPendingDone(ctx, index.PendingDone{
		Path: "/a.bin", MessageID: 55, DocumentID: 900, AccessHash: 7,
		FileReference: []byte("ref"), Size: 5, Sha256: helloSHA, Mtime: 1,
	}); err != nil {
		t.Fatal(err)
	}

	st := newFakeStore()
	fsys := New(ctx, idx, st, 1<<20)
	q := attachQueue(t, fsys)
	if err := fsys.ResumePending(); err != nil {
		t.Fatal(err)
	}
	waitIdle(t, q)

	if len(st.uploadKeys) != 0 {
		t.Fatalf("已上传过的不应重传，实际 %v", st.uploadKeys)
	}
	n, ok, err := idx.NodeByPath(ctx, "/a.bin")
	if err != nil || !ok || n.ID != 55 || n.DocumentID != 900 || n.Sha256 != helloSHA {
		t.Fatalf("应按已上传记账补索引: ok=%v err=%v node=%+v", ok, err, n)
	}
	if rows, _ := idx.ListPending(ctx); len(rows) != 0 {
		t.Fatalf("账本应清空，实际 %+v", rows)
	}
	if _, ok, _ := idx.GetPendingDone(ctx, "/a.bin"); ok {
		t.Fatal("已上传记账应清空")
	}
	if _, err := os.Stat(tmp); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("临时文件应删除: %v", err)
	}
}

// TestResumePendingDropsMissingTemp 临时文件已不存在（被清扫/损坏）时只清账，不上传。
func TestResumePendingDropsMissingTemp(t *testing.T) {
	idx := newResumeIndex(t)
	ctx := context.Background()

	gone := filepath.Join(t.TempDir(), "no-such-temp")
	if err := idx.PutPending(ctx, index.PendingUpload{Path: "/gone.bin", Temp: gone, Size: 5, Mtime: 1}); err != nil {
		t.Fatal(err)
	}

	st := newFakeStore()
	fsys := New(ctx, idx, st, 1<<20)
	q := attachQueue(t, fsys)
	if err := fsys.ResumePending(); err != nil {
		t.Fatal(err)
	}
	waitIdle(t, q)

	if len(st.uploadKeys) != 0 {
		t.Fatalf("临时文件缺失时不应上传，实际 %v", st.uploadKeys)
	}
	rows, _ := idx.ListPending(ctx)
	if len(rows) != 0 {
		t.Fatalf("无法恢复的账本应清掉，实际 %+v", rows)
	}
}
