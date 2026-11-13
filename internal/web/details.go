package web

// 文件详情片段：点「详情」才请求，图片在这里才会加载缩略图。

import (
	"html/template"
	"net/http"
	"net/url"
	"path"

	"mytgfs/internal/i18n"
)

// detailsData 是详情片段的模板数据。
type detailsData struct {
	Labels  pageLabels
	Name    string
	Path    string
	Size    string
	Mod     string
	IsImage bool
	Thumb   template.URL
	Message string
}

// handleFragDetails 输出单个文件的详情（GET /frag/details?vol=&path=）。
//
// 输入：vol 卷名；path 卷内路径
// 输出：#detail 片段
// 预期：
//   - 只有图片扩展名才在 <img> 里引用 /files/thumb，别让浏览器预取整批图片
//   - 取不到条目时返回错误文案，不返回 500
func handleFragDetails(api *filesAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		data := detailsData{Labels: loadLabels(false)}
		if api == nil {
			data.Message = i18n.T("web.files_unavailable")
			renderDetails(w, data)
			return
		}
		q := r.URL.Query()
		vol, p := q.Get("vol"), q.Get("path")
		m, ok := api.manager(vol)
		if !ok {
			data.Message = i18n.T("err.fm_no_volume")
			renderDetails(w, data)
			return
		}
		e, err := m.Info(r.Context(), p)
		if err != nil {
			data.Message = fmError(err)
			renderDetails(w, data)
			return
		}
		data.Name = e.Name
		data.Path = p
		data.Size = formatBytes(e.Size)
		data.Mod = formatMod(e.Mod)
		if !e.Dir && thumbExts[path.Ext(p)] && e.Size <= thumbMaxSource {
			data.IsImage = true
			qs := url.Values{}
			qs.Set("vol", vol)
			qs.Set("path", p)
			data.Thumb = template.URL("/files/thumb?" + qs.Encode())
		}
		renderDetails(w, data)
	}
}

// renderDetails 输出详情片段。
func renderDetails(w http.ResponseWriter, data detailsData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := pageTmpl.ExecuteTemplate(w, "details", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
