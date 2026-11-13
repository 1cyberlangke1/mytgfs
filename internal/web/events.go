package web

// 服务端事件通道：前端用 EventSource 订阅，取代定时轮询。
//
// 主题：status（文件/进度变化）、config（配置保存）、mount（WebDAV 开关）。
// 事件只带主题名，前端收到后自行拉取最新快照。

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	"mytgfs/internal/eventbus"
)

// eventTopics 是面板关心的全部主题。
var eventTopics = []string{"status", "config", "mount"}

// handleAPIEvents 输出 text/event-stream。
//
// 输入：GET /api/events
// 输出：SSE 流，形如 "event: status\ndata: {}\n\n"
// 预期：客户端断开即返回；Bus 为 nil 时 501；25s 一次心跳防代理超时
func handleAPIEvents(bus *eventbus.Bus) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			replyJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if bus == nil {
			replyJSONError(w, http.StatusNotImplemented, "events unavailable")
			return
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			replyJSONError(w, http.StatusInternalServerError, "stream unsupported")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)

		// 每个主题一个订阅，扇入到 merged，主循环只处理一个通道
		merged := make(chan string, 16)
		var wg sync.WaitGroup
		stop := make([]func(), 0, len(eventTopics))
		for _, topic := range eventTopics {
			ch, cancel := bus.Subscribe(topic)
			stop = append(stop, cancel)
			wg.Go(func() {
				for range ch {
					select {
					case merged <- topic:
					default: // 满则丢弃：前端拿到任一事件就会拉最新快照
					}
				}
			})
		}
		defer func() {
			for _, cancel := range stop {
				cancel()
			}
			wg.Wait()
		}()

		fmt.Fprint(w, "event: ready\ndata: {}\n\n")
		flusher.Flush()

		heartbeat := time.NewTicker(25 * time.Second)
		defer heartbeat.Stop()

		for {
			select {
			case <-r.Context().Done():
				return
			case topic := <-merged:
				fmt.Fprintf(w, "event: %s\ndata: {}\n\n", topic)
				flusher.Flush()
			case <-heartbeat.C:
				fmt.Fprint(w, ": ping\n\n")
				flusher.Flush()
			}
		}
	}
}
