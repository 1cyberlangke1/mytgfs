package web

// 内嵌播放：视频 / 音频 / PDF / 图片直接放进浮层，靠 Range 边下边播。
//
// 为什么不做独立页面：面板是 htmx 单页，浮层比跳页少一次往返，
// 关掉浮层不用回退浏览器历史。

import (
	"html/template"
	"net/http"
	"net/url"
	"path"

	"mytgfs/internal/i18n"
)

// playData 是播放浮层的模板数据。
type playData struct {
	Labels   pageLabels
	Name     string
	Kind     string
	Src      template.URL
	Download template.URL
	Size     string
	// Msg 是缺失/取不到时的提示文案
	Msg     string
	Missing bool
}

// handleFragPlay 渲染播放浮层（GET /frag/play?vol=&path=）。
//
// 输入：查询串 vol 卷名、path 卷内路径
// 输出：播放片段；不支持的扩展名返回提示而不是 404
// 预期：只读打开一次拿元数据，不读文件内容
func handleFragPlay(api *filesAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if api == nil {
			http.Error(w, i18n.T("web.files_unavailable"), http.StatusNotImplemented)
			return
		}
		q := r.URL.Query()
		vol, p := q.Get("vol"), q.Get("path")
		data := playData{Labels: loadLabels(false), Name: path.Base(p), Kind: mediaKind(p)}
		m, ok := api.manager(vol)
		if !ok {
			data.Missing = true
			renderPlay(w, data)
			return
		}
		f, err := m.Open(r.Context(), p)
		if err != nil {
			data.Missing = true
			renderPlay(w, data)
			return
		}
		fi, serr := f.Stat()
		_ = f.Close()
		if serr != nil {
			data.Missing = true
			renderPlay(w, data)
			return
		}
		data.Size = formatBytes(fi.Size())
		data.Name = fi.Name()
		data.Src = template.URL("/files/download?" + values(map[string]string{
			"vol": vol, "path": p, "inline": "1",
		}))
		data.Download = downloadURL(vol, p)
		renderPlay(w, data)
	}
}

// values 拼查询串。
func values(kv map[string]string) string {
	q := url.Values{}
	for k, v := range kv {
		q.Set(k, v)
	}
	return q.Encode()
}

// renderPlay 输出播放片段。
func renderPlay(w http.ResponseWriter, data playData) {
	if data.Missing && data.Msg == "" {
		data.Msg = i18n.T("err.fm_not_found")
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := pageTmpl.ExecuteTemplate(w, "play", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
