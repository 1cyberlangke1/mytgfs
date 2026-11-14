package web

// 网页版的事件通道：前端用 EventSource 订阅。
//
// 主题清单、节流、扇出都在 internal/push；这里只是其中一个传输通道，
// 桌面壳是另一个（Wails Event.Emit），两端因此共用同一份契约。

import (
	"fmt"
	"net/http"
	"time"

	"mytgfs/internal/i18n"
	"mytgfs/internal/push"
)

// handleAPIEvents 输出 text/event-stream。
//
// 输入：GET /api/events；hub 推送分发点
// 输出：SSE 流，形如 "event: status\ndata: {}\n\n"
// 预期：客户端断开即退订并返回；hub 为 nil 时 501；25s 一次心跳防代理超时
func handleAPIEvents(hub *push.Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			replyJSONError(w, http.StatusMethodNotAllowed, i18n.T("err.web_method"))
			return
		}
		if hub == nil {
			replyJSONError(w, http.StatusNotImplemented, i18n.T("err.web_events_unavailable"))
			return
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			replyJSONError(w, http.StatusInternalServerError, i18n.T("err.web_stream_unsupported"))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)

		// 订阅回调必须非阻塞：客户端卡住时丢事件，前端下一次拉快照即可
		events := make(chan string, 16)
		cancel := hub.Subscribe(func(topic string) {
			select {
			case events <- topic:
			default:
			}
		})
		defer cancel()

		fmt.Fprint(w, "event: ready\ndata: {}\n\n")
		flusher.Flush()

		heartbeat := time.Tick(25 * time.Second)
		for {
			select {
			case <-r.Context().Done():
				return
			case topic := <-events:
				fmt.Fprintf(w, "event: %s\ndata: {}\n\n", topic)
				flusher.Flush()
			case <-heartbeat:
				fmt.Fprint(w, ": ping\n\n")
				flusher.Flush()
			}
		}
	}
}
