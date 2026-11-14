package web

// 操作队列的写接口：目前只有「清空已结束记录」。
//
// 输入：DELETE /api/queue
// 输出：{"removed": n}；入口没接线时 501
// 预期：只删 done/failed，排队中和执行中的任务不受影响

import (
	"net/http"

	"mytgfs/internal/i18n"
)

// handleAPIQueue 清空已结束的队列记录。
//
// 输入：opts.ClearQueue
// 输出：JSON {removed}
// 预期：让面板的「清空记录」真正落到持久化表，而不是只改本地数组
func handleAPIQueue(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			replyJSONError(w, http.StatusMethodNotAllowed, i18n.T("err.web_method"))
			return
		}
		if opts.ClearQueue == nil {
			replyJSONError(w, http.StatusNotImplemented, i18n.T("err.queue_unavailable"))
			return
		}
		n, err := opts.ClearQueue(r.Context())
		if err != nil {
			replyJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		replyJSON(w, http.StatusOK, map[string]any{"removed": n})
	}
}
