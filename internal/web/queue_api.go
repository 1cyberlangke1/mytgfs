package web

// 操作队列的写接口。
//
// 输入：
//   - DELETE /api/queue            清空已结束记录
//   - DELETE /api/queue/{vol}/{id} 取消某卷的一条任务
// 输出：{"removed": n} / {"cancelled": true}；入口没接线时 501
// 预期：
//   - 清空只删 done/failed/cancelled，排队中和执行中的任务不受影响
//   - 取消排队中任务即刻生效；取消执行中任务打断 handler，已完成的子步骤不回滚

import (
	"errors"
	"net/http"
	"strconv"

	"mytgfs/internal/i18n"
	"mytgfs/internal/queue"
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

// handleAPIQueueCancel 取消一条排队中/执行中的任务。
//
// 输入：路径参数 {vol}/{id}；opts.CancelQueue
// 输出：JSON {cancelled: true}；id 非法 400，任务不存在 404，已结束 409
// 预期：前端点「取消」后任务不再重排；404/409 都带人话原因，面板直接显示
func handleAPIQueueCancel(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			replyJSONError(w, http.StatusMethodNotAllowed, i18n.T("err.web_method"))
			return
		}
		if opts.CancelQueue == nil {
			replyJSONError(w, http.StatusNotImplemented, i18n.T("err.queue_unavailable"))
			return
		}
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id <= 0 {
			replyJSONError(w, http.StatusBadRequest, i18n.T("err.queue_job_id"))
			return
		}
		vol := r.PathValue("vol")
		if cerr := opts.CancelQueue(r.Context(), vol, id); cerr != nil {
			switch {
			case errors.Is(cerr, queue.ErrJobNotFound):
				replyJSONError(w, http.StatusNotFound, i18n.T("err.queue_job_missing"))
			case errors.Is(cerr, queue.ErrJobFinished):
				replyJSONError(w, http.StatusConflict, i18n.T("err.queue_job_finished"))
			default:
				replyJSONError(w, http.StatusInternalServerError, cerr.Error())
			}
			return
		}
		replyJSON(w, http.StatusOK, map[string]any{"cancelled": true})
	}
}
