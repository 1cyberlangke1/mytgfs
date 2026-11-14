package push

// Hub 是进程内唯一的推送分发点。
//
// 分工：
//   - 写操作：只认 Hub（它实现 Events），统一带节流地通知
//   - 传输通道：只认 Hub.Subscribe，桌面壳注册「转 Wails 事件」，
//     网页版注册「写进 SSE 连接」
//
// 这样「主题清单」「节流」「扇出」只有一份实现，新增一种传输只是多一个订阅者；
// 写路径也拿不到底层总线，忘推送或私开通道都会被 contract_test 拦住。

import (
	"context"
	"sync"

	"mytgfs/internal/eventbus"
)

// Hub 把事件总线包装成唯一的发布/订阅入口。
type Hub struct {
	bus    *eventbus.Bus
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu    sync.Mutex
	next  int
	sinks map[int]func(topic string)
}

// NewHub 创建分发点并订阅全部主题。
//
// 输入：ctx 生命周期，取消即停止分发
// 输出：*Hub
// 预期：返回后即可 Publish/Subscribe；Close 后不再回调任何订阅者
func NewHub(ctx context.Context) *Hub {
	runCtx, cancel := context.WithCancel(ctx)
	h := &Hub{
		bus:    eventbus.New(),
		cancel: cancel,
		sinks:  make(map[int]func(topic string)),
	}
	for _, topic := range Topics {
		ch, unsubscribe := h.bus.Subscribe(topic)
		h.wg.Go(func() {
			defer unsubscribe()
			for {
				select {
				case <-runCtx.Done():
					return
				case _, ok := <-ch:
					if !ok {
						return
					}
					h.dispatch(topic)
				}
			}
		})
	}
	return h
}

// Publish 实现 Events：按最小间隔通知所有订阅者。
//
// 输入：topic 主题名
// 输出：无
// 预期：nil Hub 是空实现（命令行单跑场景）；高频调用被节流，但最后一次一定送达
func (h *Hub) Publish(topic string) {
	if h == nil {
		return
	}
	h.bus.PublishEvery(topic, DefaultGap)
}

// Subscribe 注册一个传输通道。
//
// 输入：fn 收到主题名的回调；必须非阻塞，慢操作请自己排队
// 输出：取消订阅函数
// 预期：回调在分发协程里串行执行；取消后再也不会被调用
func (h *Hub) Subscribe(fn func(topic string)) func() {
	if h == nil || fn == nil {
		return func() {}
	}
	h.mu.Lock()
	id := h.next
	h.next++
	h.sinks[id] = fn
	h.mu.Unlock()
	return func() {
		h.mu.Lock()
		delete(h.sinks, id)
		h.mu.Unlock()
	}
}

// dispatch 把主题扇出给当前所有订阅者。
//
// 输入：topic 主题名
// 输出：无
// 预期：先快照再回调，避免回调里退订导致持锁调用
func (h *Hub) dispatch(topic string) {
	h.mu.Lock()
	sinks := make([]func(string), 0, len(h.sinks))
	for _, fn := range h.sinks {
		sinks = append(sinks, fn)
	}
	h.mu.Unlock()
	for _, fn := range sinks {
		fn(topic)
	}
}

// Close 停止分发并等待协程退出。
//
// 输入：无
// 输出：无
// 预期：可重复调用；nil Hub 直接返回
func (h *Hub) Close() {
	if h == nil {
		return
	}
	h.cancel()
	h.wg.Wait()
}
