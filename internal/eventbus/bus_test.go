package eventbus

import (
	"testing"
	"time"
)

// TestPublishSubscriber 订阅者能收到通知。
func TestPublishSubscriber(t *testing.T) {
	b := New()
	ch, cancel := b.Subscribe("cfg")
	defer cancel()
	b.Publish("cfg")
	select {
	case <-ch:
	default:
		t.Fatal("没有收到事件")
	}
}

// TestPublishOtherTopic 别的主题不受影响。
func TestPublishOtherTopic(t *testing.T) {
	b := New()
	ch, cancel := b.Subscribe("cfg")
	defer cancel()
	b.Publish("mount")
	select {
	case <-ch:
		t.Fatal("收到了无关主题")
	default:
	}
}

// TestCancelStopsDelivery 退订后通道关闭且不再收到事件。
func TestCancelStopsDelivery(t *testing.T) {
	b := New()
	ch, cancel := b.Subscribe("cfg")
	cancel()
	if _, ok := <-ch; ok {
		t.Error("退订后通道应已关闭")
	}
	b.Publish("cfg") // 不应 panic
}

// TestSlowSubscriberDoesNotBlock 无缓冲通道无人读时 Publish 也不能阻塞。
func TestSlowSubscriberDoesNotBlock(t *testing.T) {
	b := New()
	_, cancel := b.Subscribe("cfg")
	defer cancel()
	done := make(chan struct{})
	go func() {
		b.Publish("cfg")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Publish 被慢订阅者阻塞")
	}
}
