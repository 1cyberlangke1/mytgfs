package web

// 视频缩略图：转发 Telegram 自带的缩略图，并按「卷+路径+修改时间」缓存到磁盘。

import (
	"context"
	"crypto/sha1"
	"errors"
	"fmt"
	"net/http"
	"os"

	"mytgfs/internal/i18n"
)

// VideoThumbFunc 取指定文件的缩略图。
//
// 输入：ctx；volume 卷名；path 卷内路径
// 输出：缩略图字节；版本；error
// 预期：目录或不存在返回 os.ErrNotExist
type VideoThumbFunc func(ctx context.Context, volume, path string) ([]byte, string, error)

// handleFileVideoThumb 输出视频缩略图（GET /api/vthumb?vol=&path=&v=）。
//
// 输入：v 文件修改时间，仅用于让浏览器与磁盘缓存随覆盖失效
// 输出：image/jpeg；无缩略图 404；未接线 501
// 预期：命中磁盘缓存时不访问 Telegram
func handleFileVideoThumb(dir string, fn VideoThumbFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, i18n.T("err.http_method"), http.StatusMethodNotAllowed)
			return
		}
		if fn == nil {
			http.Error(w, i18n.T("err.http_no_files"), http.StatusNotImplemented)
			return
		}
		q := r.URL.Query()
		vol, p := q.Get("vol"), q.Get("path")
		if vol == "" || p == "" {
			http.Error(w, i18n.T("err.http_bad_request"), http.StatusBadRequest)
			return
		}
		key := fmt.Sprintf("vthumb-%x-%s", sha1.Sum([]byte(vol+"\x00"+p)), q.Get("v"))
		w.Header().Set("ETag", `"`+key+`"`)
		w.Header().Set("Cache-Control", "private, max-age=86400")
		if etagMatches(r.Header.Get("If-None-Match"), key) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		if b, rerr := os.ReadFile(posterPath(dir, vol, p)); rerr == nil && len(b) > 0 {
			writeThumb(w, b)
			return
		}
		data, _, err := fn(r.Context(), vol, p)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				http.Error(w, i18n.T("err.http_not_found"), http.StatusNotFound)
				return
			}
			http.Error(w, i18n.T("err.thumb_fetch"), http.StatusBadGateway)
			return
		}
		if len(data) == 0 {
			http.Error(w, i18n.T("err.thumb_missing"), http.StatusNotFound)
			return
		}
		if len(data) == 0 {
			http.Error(w, i18n.T("err.thumb_missing"), http.StatusNotFound)
			return
		}
		writeThumb(w, data)
	}
}
