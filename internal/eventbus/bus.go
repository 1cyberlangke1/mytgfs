package eventbus

// 极小的进程内事件总线。
//
// 用途：配置、挂载状态等「低频但要求立刻一致」的变更，
// 从任一处发生后推给所有订阅者（托盘、每个打开的面板标签页）。
//
// 语义：
//   - Publish 不阻塞：订阅者来不及消费就丢弃，状态类事件重发即可
//   - Subscribe 返回取消函数，调用方负责退订

import (
	"sync"
	"time"
)

// Bus 是并发安全的事件总线，零值不可用，请用 New。
type Bus struct {
	mu   sync.Mutex
	subs map[string]map[chan struct{}]struct{}
	last map[string]time.Time
}

// New 创建空总线。
func New() *Bus { return &Bus{subs: map[string]map[chan struct{}]struct{}{}} }

// Subscribe 订阅一个主题。
//
// 输入：topic 主题名
// 输出：只读通知通道；取消订阅函数
// 预期：通道带小缓冲，订阅者短暂忙时事件不丢；缓冲满才丢弃
func (b *Bus) Subscribe(topic string) (<-chan struct{}, func()) {
	ch := make(chan struct{}, 8)
	b.mu.Lock()
	set := b.subs[topic]
	if set == nil {
		set = map[chan struct{}]struct{}{}
		b.subs[topic] = set
	}
	set[ch] = struct{}{}
	b.mu.Unlock()

	return ch, func() {
		b.mu.Lock()
		if s := b.subs[topic]; s != nil {
			if _, ok := s[ch]; ok {
				delete(s, ch)
				close(ch)
			}
			if len(s) == 0 {
				delete(b.subs, topic)
			}
		}
		b.mu.Unlock()
	}
}

// PublishEvery 是带最小间隔的 Publish，用于高频进度类事件。
//
// 输入：topic 主题名；every 最小推送间隔（<=0 等价于 Publish）
// 输出：无
// 预期：间隔内的后续调用被合并丢弃；状态类事件重发即可，不会累积
func (b *Bus) PublishEvery(topic string, every time.Duration) {
	if every <= 0 {
		b.Publish(topic)
		return
	}
	now := time.Now()
	b.mu.Lock()
	if b.last == nil {
		b.last = make(map[string]time.Time)
	}
	last, ok := b.last[topic]
	if ok && now.Sub(last) < every {
		b.mu.Unlock()
		return
	}
	b.last[topic] = now
	b.mu.Unlock()
	b.Publish(topic)
}

// Publish 通知某主题的所有订阅者。
//
// 输入：topic 主题名
// 输出：无
// 预期：不阻塞；没有订阅者时直接返回
func (b *Bus) Publish(topic string) {
	b.mu.Lock()
	chans := make([]chan struct{}, 0, len(b.subs[topic]))
	for ch := range b.subs[topic] {
		chans = append(chans, ch)
	}
	b.mu.Unlock()
	for _, ch := range chans {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}
