package queue

// 队列单测：不联网，验证串行、重试、不可重试错误直接失败。

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/gotd/td/tgerr"

	"mytgfs/internal/telegramstore"
)

// TestSerialExecution 串行队列不会同时跑两个任务。
func TestSerialExecution(t *testing.T) {
	q := New(1, 0)
	var mu sync.Mutex
	running, maxRunning := 0, 0
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = q.Do(context.Background(), "job", func(context.Context) error {
				mu.Lock()
				running++
				if running > maxRunning {
					maxRunning = running
				}
				mu.Unlock()
				time.Sleep(20 * time.Millisecond)
				mu.Lock()
				running--
				mu.Unlock()
				return nil
			})
		}()
	}
	wg.Wait()
	if maxRunning != 1 {
		t.Fatalf("并发数应为 1，实际 %d", maxRunning)
	}
	if n := len(q.Snapshot()); n != 4 {
		t.Fatalf("任务数 = %d", n)
	}
}

// TestRetryTransient 瞬时错误会重试到成功。
func TestRetryTransient(t *testing.T) {
	q := New(1, 2)
	q.backoff = time.Millisecond
	attempts := 0
	err := q.Do(context.Background(), "retry", func(context.Context) error {
		attempts++
		if attempts < 3 {
			return errors.New("network hiccup")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("应重试成功: %v", err)
	}
	if attempts != 3 {
		t.Fatalf("尝试次数 = %d", attempts)
	}
	snapshot := q.Snapshot()
	if snapshot[0].State != string(stateDone) {
		t.Fatalf("状态 = %s", snapshot[0].State)
	}
}

// TestNoRetryForChannelErrors 频道失效不重试。
func TestNoRetryForChannelErrors(t *testing.T) {
	q := New(1, 3)
	q.backoff = time.Millisecond
	attempts := 0
	err := q.Do(context.Background(), "bad", func(context.Context) error {
		attempts++
		return tgerr.New(400, "CHANNEL_INVALID")
	})
	if err == nil {
		t.Fatal("应返回错误")
	}
	if attempts != 1 {
		t.Fatalf("频道错误不应重试，尝试次数 = %d", attempts)
	}
}

// TestNoRetryForTooLarge 超限不重试。
func TestNoRetryForTooLarge(t *testing.T) {
	q := New(1, 3)
	q.backoff = time.Millisecond
	attempts := 0
	if err := q.Do(context.Background(), "big", func(context.Context) error {
		attempts++
		return telegramstore.ErrTooLarge
	}); err == nil {
		t.Fatal("应返回错误")
	}
	if attempts != 1 {
		t.Fatalf("超限不应重试，尝试次数 = %d", attempts)
	}
}

// TestCancelWhileQueued 排队中取消不会执行任务。
func TestCancelWhileQueued(t *testing.T) {
	q := New(1, 0)
	block := make(chan struct{})
	started := make(chan struct{})
	go func() {
		_ = q.Do(context.Background(), "hold", func(context.Context) error {
			close(started)
			<-block
			return nil
		})
	}()
	<-started

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ran := false
	err := q.Do(ctx, "cancelled", func(context.Context) error {
		ran = true
		return nil
	})
	close(block)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("应返回取消，实际 %v", err)
	}
	if ran {
		t.Fatal("取消的任务不应执行")
	}
}

// TestRetryable 覆盖各类错误的可重试判定。
func TestRetryable(t *testing.T) {
	if Retryable(nil) {
		t.Fatal("nil 不应重试")
	}
	if !Retryable(errors.New("temporary")) {
		t.Fatal("普通错误应可重试")
	}
	if Retryable(telegramstore.ErrTooLarge) {
		t.Fatal("超限不应重试")
	}
	if Retryable(tgerr.New(400, "MESSAGE_ID_INVALID")) {
		t.Fatal("消息已删不应重试")
	}
	if Retryable(tgerr.New(400, "FILE_PART_INVALID")) {
		t.Fatal("断点失效不应在这里重试")
	}
}
