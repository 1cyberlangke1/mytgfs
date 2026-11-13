package web

// 服务端事件推送（SSE）。
//
// 配置保存、挂载开关这类低频一致性事件由服务端主动推给所有打开的页面，
// 前端不再靠定时轮询去猜「别处有没有改过」。

import (
	"fmt"
	"net/http"

	"mytgfs/internal/eventbus"
	"mytgfs/internal/i18n"
)

// handleEvents 维持一条 SSE 连接。
//
// 输入：bus 事件总线；nil 时返回 501
// 输出：text/event-stream 流
// 预期：
//   - 事件名即主题：config / mount，data 为空对象
//   - 客户端断开（ctx.Done）立即退订返回
func handleEvents(bus *eventbus.Bus) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if bus == nil {
			http.Error(w, i18n.T("err.http_events_unavailable"), http.StatusNotImplemented)
			return
		}
		fl, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, i18n.T("err.http_stream_unsupported"), http.StatusInternalServerError)
			return
		}
		h := w.Header()
		h.Set("Content-Type", "text/event-stream")
		h.Set("Cache-Control", "no-cache")
		h.Set("Connection", "keep-alive")

		cfgCh, cancelCfg := bus.Subscribe("config")
		defer cancelCfg()
		mntCh, cancelMnt := bus.Subscribe("mount")
		defer cancelMnt()

		// 先写注释帧，让浏览器立刻进入 open 状态
		fmt.Fprint(w, ": connected\n\n")
		fl.Flush()

		for {
			select {
			case <-r.Context().Done():
				return
			case <-cfgCh:
				fmt.Fprint(w, "event: config\ndata: {}\n\n")
				fl.Flush()
			case <-mntCh:
				fmt.Fprint(w, "event: mount\ndata: {}\n\n")
				fl.Flush()
			}
		}
	}
}
