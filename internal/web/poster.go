package web

// 视频封面：浏览器上传时从视频里抽一帧 JPEG，服务端按「卷+路径」存盘。
// /api/vthumb 优先返回这张封面，没有时再回落到 Telegram 缩略图。

import (
	"crypto/sha1"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"mytgfs/internal/i18n"
)

// maxPosterBytes 是浏览器上传封面的上限。
const maxPosterBytes = 8 << 20

// posterKey 返回封面的缓存键。
func posterKey(vol, p string) string {
	return fmt.Sprintf("poster-%x", sha1.Sum([]byte(vol+"\x00"+p)))
}

// posterPath 返回封面文件路径。
func posterPath(dir, vol, p string) string {
	if strings.TrimSpace(dir) == "" {
		dir = "data/thumbs"
	}
	return filepath.Join(dir, posterKey(vol, p)+".jpg")
}

// handleAPIPoster 保存浏览器抽出的视频封面。
//
// 输入：POST /api/poster?vol=&path=，body 为 JPEG
// 输出：{"ok":true}；400 参数缺失/空体；413 超限
// 预期：只写本地缓存，不改索引与 Telegram
func handleAPIPoster(dir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			replyJSONError(w, http.StatusMethodNotAllowed, i18n.T("err.web_method"))
			return
		}
		q := r.URL.Query()
		vol, p := q.Get("vol"), q.Get("path")
		if vol == "" || p == "" {
			replyJSONError(w, http.StatusBadRequest, i18n.T("err.http_bad_request"))
			return
		}
		body := http.MaxBytesReader(w, r.Body, maxPosterBytes)
		data, err := io.ReadAll(body)
		if err != nil || len(data) == 0 {
			replyJSONError(w, http.StatusBadRequest, i18n.T("err.http_bad_request"))
			return
		}
		target := posterPath(dir, vol, p)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			replyJSONError(w, http.StatusInternalServerError, i18n.T("err.http_bad_request"))
			return
		}
		if err := os.WriteFile(target, data, 0o644); err != nil {
			replyJSONError(w, http.StatusInternalServerError, i18n.T("err.http_bad_request"))
			return
		}
		replyJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}
