package web

// 面板的文件管理：列出、上传、新建目录、移动、删除、下载。
//
// 全部基于 internal/fm（它在 WebDAV 层之上补齐了递归删除与跨目录移动）。
// 交互仍然走 htmx：每个操作返回浏览器片段，就地替换 #browser。

import (
	"errors"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"

	"mytgfs/internal/eventbus"
	"mytgfs/internal/fm"
	"mytgfs/internal/i18n"
)

// filesAPI 把「按卷取后端」的能力打包给 handler 用。
//
// manager 为 nil 表示当前模式不提供文件管理（例如只读面板）。
type filesAPI struct {
	manager func(volume string) (fm.Files, bool)
	volumes func() []string
	// stats 可选：返回卷内文件数与已用字节，用于卷标签上的用量显示
	stats  func(volume string) (files, used int64, ok bool)
	thumbs *thumbCache
	// bus 可选：变更后推 status 事件，其它标签页与托盘跟着刷新
	bus *eventbus.Bus
}

// setBus 挂上事件总线，供变更后广播。
func (api *filesAPI) setBus(bus *eventbus.Bus) {
	if api != nil {
		api.bus = bus
	}
}

// publishStatus 广播「状态变了」；没有总线时静默。
func (api *filesAPI) publishStatus() {
	if api != nil && api.bus != nil {
		api.bus.Publish("status")
	}
}

// newFilesAPI 组装文件管理能力；缺任一项都退化成「不提供」。
//
// 输入：manager 按卷取后端；volumes 卷名列表；stats 卷用量（可为 nil）；thumbDir 缩略图缓存目录
// 输出：*filesAPI；前两项任一为 nil 时返回 nil
// 预期：nil 时所有文件接口返回 501，页面不渲染文件卡片
func newFilesAPI(manager func(volume string) (fm.Files, bool), volumes func() []string, stats func(volume string) (int64, int64, bool), thumbDir string) *filesAPI {
	if manager == nil || volumes == nil {
		return nil
	}
	return &filesAPI{manager: manager, volumes: volumes, stats: stats, thumbs: newThumbCache(thumbDir)}
}

// handleFragFiles 列出目录（GET /frag/files?vol=&dir=）。
func handleFragFiles(api *filesAPI) http.HandlerFunc {
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
		renderBrowser(w, api.browser(r, q.Get("vol"), q.Get("dir"), "", ""))
	}
}

// handleFragFilesUpload 上传一个或多个文件（multipart）。
//
// 输入：form 字段 vol、dir、rel（可选，相对路径，按文件出现顺序对应）、quiet（可选）
// 输出：浏览器片段；quiet=1 时返回 204
// 预期：
//   - rel 让整目录上传保留子目录结构；缺省时用文件名落到当前目录
//   - 逐个上传，单个失败不影响其余文件；有失败时整体报错并给出首个原因
func handleFragFilesUpload(api *filesAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if api == nil {
			http.Error(w, i18n.T("web.files_unavailable"), http.StatusNotImplemented)
			return
		}
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			renderBrowser(w, api.browser(r, r.FormValue("vol"), r.FormValue("dir"), err.Error(), "err"))
			return
		}
		vol, dir := r.FormValue("vol"), r.FormValue("dir")
		quiet := r.FormValue("quiet") != ""
		m, ok := api.manager(vol)
		if !ok {
			if quiet {
				http.Error(w, i18n.T("err.fm_no_volume"), http.StatusNotFound)
				return
			}
			renderBrowser(w, api.browser(r, vol, dir, i18n.T("err.fm_no_volume"), "err"))
			return
		}
		rels := r.MultipartForm.Value["rel"]
		okCount := 0
		var firstErr error
		for i, fh := range r.MultipartForm.File["file"] {
			rel := ""
			if i < len(rels) {
				rel = rels[i]
			}
			target, terr := uploadTarget(dir, rel, fh.Filename)
			if terr != nil {
				if firstErr == nil {
					firstErr = terr
				}
				continue
			}
			src, err := fh.Open()
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			err = m.Upload(r.Context(), target, src)
			_ = src.Close()
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			okCount++
		}
		if quiet {
			if firstErr != nil {
				http.Error(w, fmError(firstErr), http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if firstErr != nil {
			renderBrowser(w, api.browser(r, vol, dir, fmError(firstErr), "err"))
			return
		}
		renderBrowser(w, api.browser(r, vol, dir, i18n.T("web.files_uploaded", okCount), "ok"))
	}
}

// handleFragFilesMkdir 新建目录（form: vol、dir、name）。
func handleFragFilesMkdir(api *filesAPI) http.HandlerFunc {
	return api.formAction(func(r *http.Request) (vol, dir, msg string, err error) {
		vol, dir = r.FormValue("vol"), r.FormValue("dir")
		name := safeName(r.FormValue("name"))
		if name == "" {
			return vol, dir, "", errors.New(i18n.T("web.files_name_required"))
		}
		m, ok := api.manager(vol)
		if !ok {
			return vol, dir, "", errors.New(i18n.T("err.fm_no_volume"))
		}
		if err := m.Mkdir(r.Context(), path.Join(dir, name)); err != nil {
			return vol, dir, "", err
		}
		return vol, dir, i18n.T("web.files_created", name), nil
	})
}

// handleFragFilesRemove 删除文件或目录（form: vol、dir、path）。
func handleFragFilesRemove(api *filesAPI) http.HandlerFunc {
	return api.formAction(func(r *http.Request) (vol, dir, msg string, err error) {
		vol, dir = r.FormValue("vol"), r.FormValue("dir")
		m, ok := api.manager(vol)
		if !ok {
			return vol, dir, "", errors.New(i18n.T("err.fm_no_volume"))
		}
		p := r.FormValue("path")
		if err := m.Remove(r.Context(), p); err != nil {
			return vol, dir, "", err
		}
		return vol, dir, i18n.T("web.files_deleted", path.Base(p)), nil
	})
}

// handleFragFilesMove 移动/改名（form: vol、dir、from、to）。
//
// to 可以是绝对路径，也可以是相对当前目录的名字。
func handleFragFilesMove(api *filesAPI) http.HandlerFunc {
	return api.formAction(func(r *http.Request) (vol, dir, msg string, err error) {
		vol, dir = r.FormValue("vol"), r.FormValue("dir")
		m, ok := api.manager(vol)
		if !ok {
			return vol, dir, "", errors.New(i18n.T("err.fm_no_volume"))
		}
		from := r.FormValue("from")
		to := strings.TrimSpace(r.FormValue("to"))
		if to == "" {
			return vol, dir, "", errors.New(i18n.T("web.files_target_required"))
		}
		if !strings.HasPrefix(to, "/") {
			name := safeName(to)
			if name == "" {
				return vol, dir, "", errors.New(i18n.T("web.files_target_required"))
			}
			to = path.Join(dir, name)
		}
		if err := m.Move(r.Context(), from, to); err != nil {
			return vol, dir, "", err
		}
		return vol, dir, i18n.T("web.files_moved", path.Base(to)), nil
	})
}

// handleFragFilesBatch 批量删除或移动（form: vol、dir、action、paths 多个、to）。
//
// 输入：action=delete 直接删；action=move 把所有选中项移进 to 目录
// 输出：浏览器片段
// 预期：任何一个失败就停下并报首个错误，已完成的不会回滚
func handleFragFilesBatch(api *filesAPI) http.HandlerFunc {
	return api.formAction(func(r *http.Request) (vol, dir, msg string, err error) {
		vol, dir = r.FormValue("vol"), r.FormValue("dir")
		m, ok := api.manager(vol)
		if !ok {
			return vol, dir, "", errors.New(i18n.T("err.fm_no_volume"))
		}
		paths := r.Form["paths"]
		if len(paths) == 0 {
			return vol, dir, "", errors.New(i18n.T("web.files_select_required"))
		}
		switch r.FormValue("action") {
		case "delete":
			for _, p := range paths {
				if err := m.Remove(r.Context(), p); err != nil {
					return vol, dir, "", err
				}
			}
			return vol, dir, i18n.T("web.files_deleted_n", len(paths)), nil
		case "move":
			to := strings.TrimSpace(r.FormValue("to"))
			if to == "" {
				return vol, dir, "", errors.New(i18n.T("web.files_target_required"))
			}
			if !strings.HasPrefix(to, "/") {
				to = path.Join(dir, to)
			}
			for _, p := range paths {
				if err := m.Move(r.Context(), p, path.Join(to, path.Base(p))); err != nil {
					return vol, dir, "", err
				}
			}
			return vol, dir, i18n.T("web.files_moved_n", len(paths)), nil
		}
		return vol, dir, "", errors.New(i18n.T("web.files_action_required"))
	})
}

// handleFileDownload 流式下载（GET /files/download?vol=&path=）。
//
// 支持 Range：http.ServeContent 会按请求头切片，大文件可续传。
func handleFileDownload(api *filesAPI) http.HandlerFunc {
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
		m, ok := api.manager(q.Get("vol"))
		if !ok {
			http.Error(w, i18n.T("err.fm_no_volume"), http.StatusNotFound)
			return
		}
		f, err := m.Open(r.Context(), q.Get("path"))
		if err != nil {
			http.Error(w, fmError(err), http.StatusNotFound)
			return
		}
		defer f.Close()
		fi, err := f.Stat()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		// filename* 才能正确传中文名
		if q.Get("inline") != "" {
			// 播放器要内嵌播放，附件头会让部分浏览器拒绝播放
			w.Header().Set("Content-Disposition", "inline; filename*=UTF-8''"+url.PathEscape(fi.Name()))
		} else {
			w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(fi.Name()))
		}
		http.ServeContent(w, r, fi.Name(), fi.ModTime(), f)
	}
}

// formAction 收拢「解析表单 → 干活 → 回渲染浏览器」这个重复形状。
//
// 输入：fn 返回 vol、dir、成功文案与错误
// 输出：handler
// 预期：所有错误都以 200 + err 样式返回，htmx 才会替换片段
func (api *filesAPI) formAction(fn func(r *http.Request) (vol, dir, msg string, err error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if api == nil {
			http.Error(w, i18n.T("web.files_unavailable"), http.StatusNotImplemented)
			return
		}
		if err := r.ParseForm(); err != nil {
			renderBrowser(w, api.browser(r, r.FormValue("vol"), r.FormValue("dir"), err.Error(), "err"))
			return
		}
		vol, dir, msg, err := fn(r)
		if err != nil {
			renderBrowser(w, api.browser(r, vol, dir, fmError(err), "err"))
			return
		}
		if msg == "" {
			msg = i18n.T("web.files_done")
		}
		api.publishStatus()
		renderBrowser(w, api.browser(r, vol, dir, msg, "ok"))
	}
}

// browser 组装浏览器片段的数据。
//
// 输入：vol 卷名（空则用第一个）；dir 目录；msg/kind 操作反馈
// 输出：browserData
// 预期：卷或目录取不到时不 panic，只在片段里给出提示
func (api *filesAPI) browser(r *http.Request, vol, dir, msg, kind string) browserData {
	data := browserData{Labels: loadLabels(true), Volume: vol, Dir: normalizeDir(dir), Message: msg, MsgKind: kind}
	data.Volumes = api.volumes()
	if len(data.Volumes) == 0 {
		data.Message, data.MsgKind = i18n.T("web.files_no_volume"), "err"
		return data
	}
	if data.Volume == "" {
		data.Volume = data.Volumes[0]
	}
	data.Stats = api.volumeStats(data.Volumes, data.Volume)
	m, ok := api.manager(data.Volume)
	if !ok {
		data.Message, data.MsgKind = i18n.T("err.fm_no_volume"), "err"
		return data
	}
	data.Available = true
	data.Crumbs = crumbsOf(data.Dir)
	data.Search = strings.TrimSpace(r.FormValue("q"))
	data.Sort = normalizeSort(r.FormValue("sort"))
	data.Desc = r.FormValue("desc") != ""
	if data.Search != "" {
		data.SearchQuery = "&q=" + url.QueryEscape(data.Search)
	}
	data.SortQuery = "&sort=" + data.Sort
	if data.Search != "" {
		data.SortQuery += "&q=" + url.QueryEscape(data.Search)
	}
	if data.Desc {
		data.SortQuery += "&desc=1"
	}

	var rows []fileRow
	if data.Search != "" {
		// 搜索走索引递归匹配，命中可能不在当前目录，因此带上完整路径
		hits, err := m.Search(r.Context(), data.Dir, data.Search, filesPageSize*5)
		if err != nil {
			data.Message, data.MsgKind = fmError(err), "err"
			return data
		}
		for _, h := range hits {
			note := path.Dir(h.Path)
			if note == "/" {
				note = ""
			}
			rows = append(rows, newRow(data.Volume, h.Path, path.Base(h.Path), h.Size, h.Mod, h.Dir, note))
		}
	} else {
		entries, err := m.List(r.Context(), data.Dir)
		// 删掉当前目录最后一项后目录就不存在了：向上找到最近的现存父目录再列
		for err != nil && errors.Is(err, os.ErrNotExist) && data.Dir != "/" {
			data.Dir = path.Dir(data.Dir)
			data.Crumbs = crumbsOf(data.Dir)
			entries, err = m.List(r.Context(), data.Dir)
		}
		if err != nil {
			data.Message, data.MsgKind = fmError(err), "err"
			return data
		}
		sortEntries(entries, data.Sort, data.Desc)
		for _, e := range entries {
			p := path.Join(data.Dir, e.Name)
			rows = append(rows, newRow(data.Volume, p, e.Name, e.Size, e.Mod, e.Dir, ""))
		}
	}

	// 分页：页码来自查询串（GET）或表单（POST），非法值一律回落到第 1 页
	page := 1
	if n, perr := strconv.Atoi(r.FormValue("page")); perr == nil && n > 0 {
		page = n
	}
	data.Total = len(rows)
	data.Pages = max((data.Total+filesPageSize-1)/filesPageSize, 1)
	if page > data.Pages {
		page = data.Pages
	}
	data.Page = page
	start := (page - 1) * filesPageSize
	end := min(start+filesPageSize, data.Total)
	if start > data.Total {
		start = data.Total
	}
	if data.Page > 1 {
		data.PrevPage = data.Page - 1
	}
	if data.Page < data.Pages {
		data.NextPage = data.Page + 1
	}
	data.Rows = rows[start:end]
	return data
}
