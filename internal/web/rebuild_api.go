package web

// 重建索引接口：POST /api/rebuild?vol=<卷名>。
//
// 面板只负责触发；拉历史与写索引都在装配根完成。

import (
	"context"
	"net/http"

	"mytgfs/internal/i18n"
)

// RebuildFunc 按卷名重建本地索引。
//
// 输入：ctx；volume 卷名
// 输出：可序列化结果（含写入节点数）；error
// 预期：卷不存在或没有 user 会话时返回错误，不静默成功
type RebuildFunc func(ctx context.Context, volume string) (any, error)

// handleAPIRebuild 触发指定卷的索引重建。
//
// 输入：opts.Rebuild 由装配根注入
// 输出：POST {"vol":"demo"} → {"files":N}；501 未接线
// 预期：只接受 POST；卷名从查询参数取，避免路径拼接
func handleAPIRebuild(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			replyJSONError(w, http.StatusMethodNotAllowed, i18n.T("err.web_method"))
			return
		}
		if opts.Rebuild == nil {
			replyJSONError(w, http.StatusNotImplemented, i18n.T("err.rebuild_unavailable"))
			return
		}
		vol := r.URL.Query().Get("vol")
		if vol == "" {
			replyJSONError(w, http.StatusBadRequest, i18n.T("err.fm_bad_path"))
			return
		}
		res, err := opts.Rebuild(r.Context(), vol)
		if err != nil {
			replyJSONError(w, http.StatusBadGateway, err.Error())
			return
		}
		replyJSON(w, http.StatusOK, res)
	}
}
