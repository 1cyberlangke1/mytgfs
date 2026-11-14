package push

// 推送契约单测：主题清单、桌面事件名、Hub 扇出与退订、记录器。

import (
	"slices"
	"sync"
	"testing"
	"time"
)

// TestTopicsCoverContract 固定主题清单。
//
// 输入：无
// 输出：Topics 至少包含 status 与 config
// 预期：网页版 SSE 与桌面壳共用这份清单，删主题会让两端同时失效
func TestTopicsCoverContract(t *testing.T) {
	for _, want := range []string{TopicStatus, TopicConfig} {
		if !slices.Contains(Topics, want) {
			t.Fatalf("Topics 缺少 %q: %v", want, Topics)
		}
	}
}

// TestDesktopEventName 固定桌面事件名前缀。
//
// 输入：status
// 输出：mytgfs:status
// 预期：前端 events.ts 的 desktopEvent 必须给出同样结果
func TestDesktopEventName(t *testing.T) {
	if got := DesktopEvent(TopicStatus); got != "mytgfs:status" {
		t.Fatalf("DesktopEvent=%q", got)
	}
}

// sinkRecorder 记录订阅者收到的主题。
type sinkRecorder struct {
	mu     sync.Mutex
	topics []string
}

func (s *sinkRecorder) collect(topic string) {
	s.mu.Lock()
	s.topics = append(s.topics, topic)
	s.mu.Unlock()
}

func (s *sinkRecorder) contains(topic string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Contains(s.topics, topic)
}

// TestHubFansOutToAllSinks 一次发布要送到所有传输通道。
//
// 输入：注册两个订阅者，推送 status
// 输出：两个订阅者都收到
// 预期：桌面壳与网页版因此共用同一份推送，不会漏掉其中一端
func TestHubFansOutToAllSinks(t *testing.T) {
	hub := NewHub(t.Context())
	defer hub.Close()

	desktop, web := &sinkRecorder{}, &sinkRecorder{}
	hub.Subscribe(desktop.collect)
	hub.Subscribe(web.collect)

	hub.Publish(TopicStatus)

	waitUntil(t, func() bool { return desktop.contains(TopicStatus) && web.contains(TopicStatus) })
}

// TestHubUnsubscribe 退订后不再收到事件。
//
// 输入：订阅后立即退订，再推送
// 输出：订阅者没有收到
// 预期：SSE 连接断开后不会往已关闭的通道写
func TestHubUnsubscribe(t *testing.T) {
	hub := NewHub(t.Context())
	defer hub.Close()

	sink := &sinkRecorder{}
	cancel := hub.Subscribe(sink.collect)
	cancel()
	hub.Publish(TopicStatus)

	time.Sleep(100 * time.Millisecond)
	if len(sink.topics) != 0 {
		t.Fatalf("退订后仍收到 %v", sink.topics)
	}
}

// TestHubNilSafe nil Hub 的所有方法都可安全调用。
//
// 输入：nil *Hub
// 输出：Publish/Subscribe/Close 不 panic
// 预期：命令行单跑没有面板时直接传 nil
func TestHubNilSafe(t *testing.T) {
	var hub *Hub
	hub.Publish(TopicStatus)
	cancel := hub.Subscribe(func(string) {})
	cancel()
	hub.Close()
}

// TestRecorder 记录器计数与清空。
//
// 输入：推两次 status、一次 config
// 输出：Count(status)=2，Reset 后归零
// 预期：并发安全，复制返回不共享切片
func TestRecorder(t *testing.T) {
	rec := NewRecorder()
	rec.Publish(TopicStatus)
	rec.Publish(TopicStatus)
	rec.Publish(TopicConfig)
	if got := rec.Count(TopicStatus); got != 2 {
		t.Fatalf("Count(status)=%d", got)
	}
	topics := rec.Topics()
	topics[0] = "mutated"
	if rec.Topics()[0] != TopicStatus {
		t.Fatal("Topics 返回了共享切片")
	}
	rec.Reset()
	if len(rec.Topics()) != 0 {
		t.Fatal("Reset 未清空")
	}
}

// waitUntil 等到条件成立，超时失败。
//
// 输入：cond 条件函数
// 输出：无
// 预期：最多等 2 秒，避免用例卡死
func waitUntil(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("等待事件超时")
}
