package queue

// 取消任务的单测：排队中直接落终态、执行中打断 handler、已结束与不存在分别报错。
// 全部用本地 SQLite 临时库，不联网。

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// waitJobState 轮询任务状态，直到命中期望值或超时。
//
// 输入：t 测试；q 队列；id 任务号；want 期望状态
// 输出：实际状态（超时也返回最后一次读到的值）
// 预期：只在测试里用轮询，正式代码靠通道唤醒
func waitJobState(t *testing.T, q *Queue, id int64, want string) string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var got string
	for time.Now().Before(deadline) {
		err := q.db.QueryRow(`SELECT state FROM jobs WHERE id = ?`, id).Scan(&got)
		if err != nil {
			t.Fatalf("query state: %v", err)
		}
		if got == want {
			return got
		}
		time.Sleep(10 * time.Millisecond)
	}
	return got
}

// TestCancelQueuedJob 排队中的任务取消后直接落 cancelled，handler 不会被执行。
func TestCancelQueuedJob(t *testing.T) {
	q := newTestQueue(t, 1)
	block := make(chan struct{})
	var ran bool
	var ranMu sync.Mutex
	q.SetHandler(func(ctx context.Context, j Job) error {
		if j.Kind == "block" {
			<-block
			return nil
		}
		ranMu.Lock()
		ran = true
		ranMu.Unlock()
		return nil
	})
	// 先占住唯一的执行槽，让待取消的任务留在排队里
	first, err := q.Enqueue(t.Context(), Job{Volume: "v1", Kind: "block", Name: "hold"})
	if err != nil {
		t.Fatalf("enqueue block: %v", err)
	}
	waitJobState(t, q, first, StateRunning)

	id, err := q.Enqueue(t.Context(), Job{Volume: "v2", Kind: "upload", Name: "b.txt"})
	if err != nil {
		t.Fatalf("enqueue target: %v", err)
	}
	if err := q.Cancel(t.Context(), id); err != nil {
		t.Fatalf("cancel queued: %v", err)
	}
	if got := waitJobState(t, q, id, StateCancelled); got != StateCancelled {
		t.Fatalf("state = %q, want cancelled", got)
	}
	close(block)
	if err := q.Wait(t.Context(), id); err != nil {
		t.Fatalf("wait cancelled: %v", err)
	}
	ranMu.Lock()
	defer ranMu.Unlock()
	if ran {
		t.Fatal("cancelled job must not run")
	}
}

// TestCancelRunningJob 执行中的任务取消会打断 handler 的 ctx，终态是 cancelled 而非 failed。
func TestCancelRunningJob(t *testing.T) {
	q := newTestQueue(t, 1)
	started := make(chan struct{})
	q.SetHandler(func(ctx context.Context, j Job) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	})
	id, err := q.Enqueue(t.Context(), Job{Volume: "v1", Kind: "upload", Name: "big.bin"})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	<-started
	if err := q.Cancel(t.Context(), id); err != nil {
		t.Fatalf("cancel running: %v", err)
	}
	if got := waitJobState(t, q, id, StateCancelled); got != StateCancelled {
		t.Fatalf("state = %q, want cancelled", got)
	}
	// 取消不是失败：Wait 不应把原因抛给调用方
	if err := q.Wait(t.Context(), id); err != nil {
		t.Fatalf("wait cancelled: %v", err)
	}
}

// TestCancelFinishedJob 已结束的任务再取消返回 ErrJobFinished。
func TestCancelFinishedJob(t *testing.T) {
	q := newTestQueue(t, 1)
	q.SetHandler(func(ctx context.Context, j Job) error { return nil })
	id, err := q.Enqueue(t.Context(), Job{Volume: "v1", Kind: "mkdir", Name: "d"})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := q.Wait(t.Context(), id); err != nil {
		t.Fatalf("wait: %v", err)
	}
	if err := q.Cancel(t.Context(), id); !errors.Is(err, ErrJobFinished) {
		t.Fatalf("cancel finished = %v, want ErrJobFinished", err)
	}
}

// TestCancelMissingJob 不存在的任务号返回 ErrJobNotFound。
func TestCancelMissingJob(t *testing.T) {
	q := newTestQueue(t, 1)
	if err := q.Cancel(t.Context(), 9999); !errors.Is(err, ErrJobNotFound) {
		t.Fatalf("cancel missing = %v, want ErrJobNotFound", err)
	}
}

// TestClearFinishedRemovesCancelled 清空记录时取消态与成功/失败一起删掉。
func TestClearFinishedRemovesCancelled(t *testing.T) {
	q := newTestQueue(t, 1)
	block := make(chan struct{})
	q.SetHandler(func(ctx context.Context, j Job) error {
		if j.Kind == "block" {
			<-block
			return nil
		}
		return nil
	})
	first, err := q.Enqueue(t.Context(), Job{Volume: "v1", Kind: "block", Name: "hold"})
	if err != nil {
		t.Fatalf("enqueue block: %v", err)
	}
	waitJobState(t, q, first, StateRunning)
	id, err := q.Enqueue(t.Context(), Job{Volume: "v2", Kind: "upload", Name: "c.txt"})
	if err != nil {
		t.Fatalf("enqueue target: %v", err)
	}
	if err := q.Cancel(t.Context(), id); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	waitJobState(t, q, id, StateCancelled)
	n, err := q.ClearFinished(t.Context())
	if err != nil {
		t.Fatalf("clear: %v", err)
	}
	if n != 1 {
		t.Fatalf("removed = %d, want 1", n)
	}
	close(block)
}
