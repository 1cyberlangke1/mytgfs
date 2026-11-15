package vfs

// 上传进度回传的单测：验证队列快照能读到真实字节数。

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"mytgfs/internal/queue"
)

// TestReporterReportsBytesToQueue 上传回调写进队列快照。
//
// 输入：一个队列任务与 reporter(id, 1000)，上报 500 字节
// 输出：快照里 done=500、size=1000
// 预期：面板的百分比来自这里，回调必须落到队列而不是只存在本地变量
func TestReporterReportsBytesToQueue(t *testing.T) {
	dsn := "file:" + filepath.Join(t.TempDir(), "q.db") + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	q, err := queue.Open(db, 1, func() {})
	if err != nil {
		t.Fatalf("open queue: %v", err)
	}
	t.Cleanup(q.Close)

	id, err := q.Enqueue(t.Context(), queue.Job{Volume: "demo", Kind: queue.KindUpload, Name: "/a.bin"})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	d := &FileSystem{q: q}
	d.reporter(id, 1000)(500)

	items := q.Snapshot()
	if len(items) != 1 {
		t.Fatalf("快照条数 = %d, 期望 1", len(items))
	}
	if items[0].Done != 500 || items[0].Size != 1000 {
		t.Fatalf("进度 = %d/%d, 期望 500/1000", items[0].Done, items[0].Size)
	}
}

// TestReporterNilWithoutQueue 未接队列时返回 nil，调用方不必判空两次。
//
// 输入：没有队列的 FileSystem
// 输出：reporter 为 nil
// 预期：vfs 在测试或 CLI 场景可能不接队列，这里不能 panic
func TestReporterNilWithoutQueue(t *testing.T) {
	d := &FileSystem{}
	if fn := d.reporter(1, 100); fn != nil {
		t.Fatal("没有队列时 reporter 应为 nil")
	}
}
