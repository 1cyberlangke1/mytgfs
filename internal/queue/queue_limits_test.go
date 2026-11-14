package queue

// 队列容量与清理规则的测试：已结束记录可清、排队任务有上限。
//
// 输入：直接构造 jobs 表中的不同状态
// 输出：ClearFinished 只删已完成/失败；Enqueue 超上限返回 ErrQueueFull
// 预期：活跃任务永远不被清理逻辑误删

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

// seedJob 直接写一行任务，绕过 handler 以便构造任意状态。
func seedJob(t *testing.T, q *Queue, state string) {
	t.Helper()
	if _, err := q.db.Exec(`
INSERT INTO jobs(volume, kind, name, payload, state, attempt, err, created_at, updated_at)
VALUES('demo', 'mkdir', '/x', '{}', ?, 0, '', 0, 0)`, state); err != nil {
		t.Fatalf("seed %s: %v", state, err)
	}
}

// newQuietQueue 只建队列不启动 worker：状态由 seedJob 直接写入，
// 避免 worker 把 queued 行领走导致行数不确定。
//
// 输入：t
// 输出：未 Start 的 *Queue
// 预期：ClearFinished/Enqueue 不依赖 worker 运行
func newQuietQueue(t *testing.T) *Queue {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "queue.db") + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	q, err := Open(db, 1, func() {})
	if err != nil {
		t.Fatalf("open queue: %v", err)
	}
	return q
}

// TestClearFinishedKeepsActive 清空记录只影响已结束任务。
//
// 输入：done/failed/queued/running 各一行
// 输出：删除 2 条，剩余 queued 与 running
// 预期：持久化表真的少行，而不是只改界面
func TestClearFinishedKeepsActive(t *testing.T) {
	q := newQuietQueue(t)
	for _, st := range []string{stateDone, stateFailed, stateQueued, stateRunning} {
		seedJob(t, q, st)
	}

	n, err := q.ClearFinished(context.Background())
	if err != nil {
		t.Fatalf("clear: %v", err)
	}
	if n != 2 {
		t.Fatalf("删除 %d 条，期望 2", n)
	}
	var left int
	if err := q.db.QueryRow(`SELECT COUNT(*) FROM jobs`).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 2 {
		t.Fatalf("剩余 %d 条，期望 2（queued/running）", left)
	}
}

// TestEnqueueRejectsWhenFull 排队任务达到上限时报错。
//
// 输入：maxPending 条排队任务后再次入队
// 输出：ErrQueueFull
// 预期：队列不会无限增长；错误可被上层翻译成提示
func TestEnqueueRejectsWhenFull(t *testing.T) {
	q := newQuietQueue(t)
	tx, err := q.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	stmt, err := tx.Prepare(`
INSERT INTO jobs(volume, kind, name, payload, state, attempt, err, created_at, updated_at)
VALUES('demo', 'mkdir', '/x', '{}', ?, 0, '', 0, 0)`)
	if err != nil {
		t.Fatal(err)
	}
	for range maxPending {
		if _, err := stmt.Exec(stateQueued); err != nil {
			t.Fatal(err)
		}
	}
	stmt.Close()
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	_, err = q.Enqueue(context.Background(), Job{Volume: "demo", Kind: "mkdir", Name: "/y"})
	if !errors.Is(err, ErrQueueFull) {
		t.Fatalf("err = %v，期望 ErrQueueFull", err)
	}
}
