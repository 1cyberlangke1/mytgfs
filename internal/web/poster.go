package web

// 视频封面：浏览器上传时从视频里抽一帧 JPEG，服务端按「卷+路径」存盘。
// /api/vthumb 优先返回这张封面，没有时再回落到 Telegram 缩略图。

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"mytgfs/internal/i18n"
)

// maxPosterBytes 是浏览器上传封面的上限。
const maxPosterBytes = 8 << 20

// safeVolumeDir 判断卷名能否安全地当作缓存目录的一段。
//
// 输入：vol 请求里的卷名
// 输出：true 表示不含路径分隔符/父目录/盘符等会改变落点或越出缓存根的内容
// 预期：缓存目录是 thumbs/<vol>，卷名一旦带 .. 或 / 就会把文件写到缓存根之外
func safeVolumeDir(vol string) bool {
	if vol == "" || vol == "." || vol == ".." {
		return false
	}
	for _, r := range vol {
		switch {
		case r < 0x20, r == '/', r == '\\', r == ':', r < 0x7f && strings.ContainsRune(`<>"|?*`, r):
			return false
		}
	}
	return true
}

// posterPath 返回浏览器封面缓存文件的路径。
//
// 布局：thumbs/<vol>/<路径哈希>-<版本>.jpg，分卷便于删卷整块清理；
// v 只允许数字（它是修改时间），防路径注入。
func posterPath(dir, vol, p, v string) string {
	if strings.TrimSpace(dir) == "" {
		dir = "data/thumbs"
	}
	if v == "" {
		v = "0"
	}
	if !onlyDigits(v) {
		v = "0"
	}
	return filepath.Join(dir, vol, pathHash(vol, p)+"-"+v+".jpg")
}

// onlyDigits 判断字符串是否全是数字。
func onlyDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// handleAPIPoster 保存浏览器抽出的视频封面。
//
// 输入：POST /api/poster?vol=&path=，body 为 JPEG
// 输出：{"ok":true}；400 参数缺失/空体；413 超限
// 预期：只写本地缓存，不改索引与 Telegram
func handleAPIPoster(dir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requirePost(w, r) {
			return
		}
		q := r.URL.Query()
		vol, p := q.Get("vol"), q.Get("path")
		if vol == "" || p == "" {
			replyJSONError(w, http.StatusBadRequest, i18n.T("err.http_bad_request"))
			return
		}
		// 卷名直接用做缓存子目录：不校验等于允许 /api/poster?vol=../../x 写到缓存根外
		if !safeVolumeDir(vol) {
			replyJSONError(w, http.StatusBadRequest, i18n.T("err.http_bad_request"))
			return
		}
		bodyFromSrc(r)
		body := http.MaxBytesReader(w, r.Body, maxPosterBytes)
		data, err := io.ReadAll(body)
		if err != nil || len(data) == 0 {
			replyJSONError(w, http.StatusBadRequest, i18n.T("err.http_bad_request"))
			return
		}
		target := posterPath(dir, vol, p, q.Get("v"))
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
