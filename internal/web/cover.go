package web

// 卷封面：把卷根目录里特殊命名的图片当作这个「盘」的显示图。
//
// 命名约定（大小写不敏感，只认卷根目录）：
//
//	.cover.jpg / .cover.png / cover.jpg / cover.png ...
//
// 服务端把它缩放到 720px 长边后写入磁盘缓存（与缩略图同一目录、同一套 key），
// 之后启动、切页、刷新都不再回 Telegram 取图。

import (
	"context"
	"net/http"
	"path"
	"strings"

	"mytgfs/internal/fm"
	"mytgfs/internal/i18n"
)

// coverStems 是允许的封面文件名（不含扩展名）。
var coverStems = map[string]bool{
	".cover": true,
	"cover":  true,
}

// isCoverName 判断文件名是否是保留的封面名。
//
// 输入：name 文件名（不含目录）
// 输出：true 表示可作为卷封面
// 预期：只认 cover/.cover 加图片扩展名；区分大小写不敏感
func isCoverName(name string) bool {
	ext := strings.ToLower(path.Ext(name))
	if !thumbExts[ext] {
		return false
	}
	stem := strings.ToLower(strings.TrimSuffix(name, path.Ext(name)))
	return coverStems[stem]
}

// findCover 在卷根目录里挑出封面文件。
//
// 输入：ctx；m 文件后端
// 输出：条目与是否找到
// 预期：多个同名（不同扩展名）时按名字排序取第一个，保证结果稳定
func findCover(ctx context.Context, m fm.Files) (fm.Entry, bool) {
	entries, err := m.List(ctx, "/")
	if err != nil {
		return fm.Entry{}, false
	}
	var best fm.Entry
	found := false
	for _, e := range entries {
		if e.Dir || !isCoverName(e.Name) {
			continue
		}
		if !found || e.Name < best.Name {
			best, found = e, true
		}
	}
	return best, found
}

// handleVolumeCover 输出卷封面（GET /api/cover?vol=）。
//
// 输入：vol 卷名
// 输出：image/jpeg；没有封面 404；命中 If-None-Match 时 304
// 预期：
//   - 首次生成走 Telegram，之后命中 data/thumbs 磁盘缓存
//   - ETag 由「卷+路径+大小+修改时间」算出，换封面自动失效
//   - 浏览器按 max-age=604800 缓存，刷新只做 304 校验
func handleVolumeCover(api *filesAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, i18n.T("err.http_method"), http.StatusMethodNotAllowed)
			return
		}
		if api == nil {
			http.Error(w, i18n.T("err.http_no_files"), http.StatusNotImplemented)
			return
		}
		vol := r.URL.Query().Get("vol")
		m, ok := api.manager(vol)
		if !ok {
			http.Error(w, i18n.T("err.http_no_volume"), http.StatusNotFound)
			return
		}
		e, ok := findCover(r.Context(), m)
		if !ok {
			http.Error(w, i18n.T("err.http_not_found"), http.StatusNotFound)
			return
		}
		if e.Size > thumbMaxSource {
			http.Error(w, i18n.T("err.http_too_large"), http.StatusUnsupportedMediaType)
			return
		}
		p := path.Join("/", e.Name)
		key := "cover-" + thumbKey(vol, p, e)
		w.Header().Set("ETag", `"`+key+`"`)
		w.Header().Set("Cache-Control", "private, max-age=604800")
		if etagMatches(r.Header.Get("If-None-Match"), key) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		if b := api.thumbs.load(key); b != nil {
			writeThumb(w, b)
			return
		}
		api.thumbs.mu.Lock()
		b := api.thumbs.load(key)
		if b == nil {
			b = generateThumb(r, m, p, coverMaxSide)
			if b != nil {
				api.thumbs.save(key, b)
			}
		}
		api.thumbs.mu.Unlock()
		if b == nil {
			http.Error(w, i18n.T("err.http_decode_failed"), http.StatusUnsupportedMediaType)
			return
		}
		writeThumb(w, b)
	}
}
