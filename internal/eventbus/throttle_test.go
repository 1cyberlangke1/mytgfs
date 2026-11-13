package eventbus

import (
	"testing"
	"time"
)

// TestPublishEveryThrottles 验证最小间隔内的连发被合并。
//
// 输入：同一主题连续三次 PublishEvery，间隔 5ms，节流窗口 50ms
// 输出：订阅者只收到一次
func TestPublishEveryThrottles(t *testing.T) {
	b := New()
	ch, cancel := b.Subscribe("status")
	defer cancel()

	for range 3 {
		b.PublishEvery("status", 50*time.Millisecond)
	}
	if len(ch) != 1 {
		t.Fatalf("收到 %d 次事件，期望 1 次", len(ch))
	}
}

// TestPublishEveryWindowExpires 验证窗口过期后可以再推。
//
// 输入：PublishEvery 一次后等待超过窗口，再推一次
// 输出：订阅者收到两次
func TestPublishEveryWindowExpires(t *testing.T) {
	b := New()
	ch, cancel := b.Subscribe("status")
	defer cancel()

	b.PublishEvery("status", 10*time.Millisecond)
	time.Sleep(20 * time.Millisecond)
	b.PublishEvery("status", 10*time.Millisecond)

	if len(ch) != 2 {
		t.Fatalf("收到 %d 次事件，期望 2 次", len(ch))
	}
}

// TestPublishEveryZeroAlwaysPublishes 验证 every<=0 退化成普通 Publish。
func TestPublishEveryZeroAlwaysPublishes(t *testing.T) {
	b := New()
	ch, cancel := b.Subscribe("status")
	defer cancel()

	b.PublishEvery("status", 0)
	b.PublishEvery("status", -time.Second)

	if len(ch) != 2 {
		t.Fatalf("收到 %d 次事件，期望 2 次", len(ch))
	}
}
