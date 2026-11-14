package queue

// 推送时机单测：入队、开始、结束都要回调，否则面板会停在旧状态。

import (
	"context"
	"sync"
	"testing"
	"time"

	"mytgfs/internal/telegramstore"
)

// notifyCounter 记录状态变化回调被调用的次数。
type notifyCounter struct {
	mu sync.Mutex
	n  int
}

// bump 自增一次。
func (c *notifyCounter) bump() {
	c.mu.Lock()
	c.n++
	c.mu.Unlock()
}

// value 返回当前次数。
func (c *notifyCounter) value() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

// TestNotifyOnEnqueueStartFinish 固定「入队→开始→结束」各推一次。
//
// 输入：一条 upload 任务，handler 立即成功
// 输出：回调次数在等待后至少 +3
// 预期：少任何一次，面板的操作队列就永远停在排队中或执行中
func TestNotifyOnEnqueueStartFinish(t *testing.T) {
	q, counter := newTestQueueWithNotify(t, 1)
	q.SetHandler(func(context.Context, Job) error { return nil })

	before := counter.value()
	id, err := q.Enqueue(t.Context(), Job{Volume: "demo", Kind: KindMkdir, Name: "/a"})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if got := counter.value(); got < before+1 {
		t.Fatalf("入队后回调=%d，期望 >= %d", got, before+1)
	}
	if err := q.Wait(t.Context(), id); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	waitNotify(t, counter, before+3)
}

// TestNotifyOnFailure 失败也要推一次：面板必须能显示失败原因。
//
// 输入：handler 返回不可重试错误（文件超限）
// 输出：任务落 failed，回调次数增加
// 预期：失败任务不会静默留在执行中
func TestNotifyOnFailure(t *testing.T) {
	q, counter := newTestQueueWithNotify(t, 1)
	q.SetHandler(func(context.Context, Job) error { return telegramstore.ErrTooLarge })

	before := counter.value()
	id, err := q.Enqueue(t.Context(), Job{Volume: "demo", Kind: KindRemove, Name: "/b"})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	// 失败由 Wait 返回错误体现；这里只关心回调次数
	_ = q.Wait(t.Context(), id)
	waitNotify(t, counter, before+3)
}

// waitNotify 等到回调次数达到 want，超时即失败。
//
// 输入：t 测试；counter 计数器；want 目标次数
// 输出：无
// 预期：轮询等待，最多 2 秒，避免用例卡死
func waitNotify(t *testing.T, counter *notifyCounter, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if counter.value() >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("回调次数=%d，期望 >= %d", counter.value(), want)
}
