package web

// 面板的文件管理：列出、上传、新建目录、移动、删除、下载。
//
// 全部基于 internal/fm（它在 WebDAV 层之上补齐了递归删除与跨目录移动）。
// 交互仍然走 htmx：每个操作返回浏览器片段，就地替换 #browser。

import (
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

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

// fileRow 是表格里的一行。
type fileRow struct {
	fm.Entry
	// Path 是卷内绝对路径，供移动/删除使用
	Path string
	// Note 是名字下面的灰色补充（搜索结果里显示所在目录）
	Note string
	// Kind 决定能否内嵌播放：video / audio / pdf / image / 空
	Kind string
	// Download 是已编码好的下载地址。
	// 用 template.URL 是因为 html/template 在 href 上下文会二次转义 %2F → %252F。
	Download template.URL
	// Play 是播放页地址，Kind 非空时才有意义
	Play template.URL
}

// volumeStat 是卷标签上显示的用量。
type volumeStat struct {
	Name   string
	Files  int64
	Used   int64
	Active bool
	// HasStats 为 false 时不显示用量（例如只读模式没有索引）
	HasStats bool
}

// crumb 是面包屑的一段。
type crumb struct {
	Name string
	Path string
}

// browserData 是文件浏览器片段的模板数据。
type browserData struct {
	Labels  pageLabels
	Volumes []string
	// Stats 与 Volumes 一一对应，用于卷标签上的用量
	Stats  []volumeStat
	Volume string
	Dir    string
	Crumbs []crumb
	Rows   []fileRow
	// 搜索与排序：Search 非空表示当前结果是递归搜索结果
	Search string
	Sort   string
	Desc   bool
	// SearchQuery / SortQuery 是拼好的查询串后缀（含前导 &），供排序按钮与翻页复用
	SearchQuery string
	SortQuery   string
	// 分页：Page 从 1 起；Pages<=1 时不渲染翻页条
	Page     int
	Pages    int
	Total    int
	PrevPage int
	NextPage int
	// Message/MsgKind 是上一次操作的反馈，空串表示不显示
	Message string
	MsgKind string
	// Available 为 false 时只显示提示，不渲染工具栏
	Available bool
}

// filesPageSize 是单页最多渲染的条目数。
//
// 目录可能有上千个文件，一次全渲染会让页面与索引查询都很重。
const filesPageSize = 200

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

// uploadTarget 算出上传落点。
//
// 输入：dir 当前目录；rel 浏览器给的相对路径（可空）；name 文件名
// 输出：卷内绝对路径；error
// 预期：
//   - rel 必须带文件名（以 / 结尾视为目录路径，直接拒绝）
//   - rel 里的 .. 段一律拒绝，防止写到卷外或其它目录
func uploadTarget(dir, rel, name string) (string, error) {
	rel = strings.TrimSpace(rel)
	if rel == "" {
		n := safeName(name)
		if n == "" {
			return "", fm.ErrBadPath
		}
		return path.Join(dir, n), nil
	}
	if strings.HasSuffix(rel, "/") || strings.HasSuffix(rel, `\`) {
		return "", fm.ErrBadPath
	}
	n, ok := cleanRelPath(rel)
	if !ok {
		return "", fm.ErrBadPath
	}
	return path.Join(dir, n), nil
}

// cleanRelPath 规范化浏览器给的相对路径。
//
// 输入：rel 形如 "sub/dir/a.txt"，允许反斜杠
// 输出：干净相对路径；含 .. 或为空时 ok=false
// 预期：保留子目录结构，但绝不包含绝对路径成分
func cleanRelPath(rel string) (string, bool) {
	rel = strings.ReplaceAll(rel, `\`, "/")
	rel = strings.TrimPrefix(rel, "/")
	var segs []string
	for _, seg := range strings.Split(rel, "/") {
		switch seg {
		case "", ".", "..":
			if seg == ".." {
				return "", false
			}
			continue
		}
		segs = append(segs, seg)
	}
	if len(segs) == 0 {
		return "", false
	}
	return strings.Join(segs, "/"), true
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
	data.Pages = (data.Total + filesPageSize - 1) / filesPageSize
	if data.Pages < 1 {
		data.Pages = 1
	}
	if page > data.Pages {
		page = data.Pages
	}
	data.Page = page
	start := (page - 1) * filesPageSize
	end := start + filesPageSize
	if end > data.Total {
		end = data.Total
	}
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

// volumeStats 给每个卷取文件数与占用，用于卷标签与汇总。
//
// 输入：names 卷名；active 当前卷
// 输出：与 names 等长的用量列表
// 预期：stats 未注入或某卷取不到时该卷 HasStats=false，不影响其余卷
func (api *filesAPI) volumeStats(names []string, active string) []volumeStat {
	out := make([]volumeStat, 0, len(names))
	for _, n := range names {
		st := volumeStat{Name: n, Active: n == active}
		if api.stats != nil {
			if files, used, ok := api.stats(n); ok {
				st.Files, st.Used, st.HasStats = files, used, true
			}
		}
		out = append(out, st)
	}
	return out
}

// newRow 组装表格行，顺带算好下载地址与可播放类型。
//
// 输入：vol 卷名；p 卷内路径；name 显示名；size/mod 元数据；isDir；note 灰色补充
// 输出：fileRow
// 预期：目录没有下载/播放地址
func newRow(vol, p, name string, size int64, mod time.Time, isDir bool, note string) fileRow {
	row := fileRow{Entry: fm.Entry{Name: name, Dir: isDir, Size: size, Mod: mod}, Path: p, Note: note}
	if isDir {
		return row
	}
	row.Download = downloadURL(vol, p)
	row.Kind = mediaKind(name)
	if row.Kind != "" {
		row.Play = playURL(vol, p)
	}
	return row
}

// mediaKind 按扩展名判断能否在浏览器里直接播。
//
// 输入：文件名
// 输出：video / audio / pdf / image / 空
// 预期：只认浏览器原生支持的常见格式，其余返回空串走下载
func mediaKind(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".mp4", ".webm", ".mkv", ".mov", ".m4v":
		return "video"
	case ".mp3", ".m4a", ".aac", ".ogg", ".opus", ".wav", ".flac":
		return "audio"
	case ".pdf":
		return "pdf"
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp", ".avif":
		return "image"
	}
	return ""
}

// normalizeSort 把排序参数收敛到固定集合。
func normalizeSort(s string) string {
	switch s {
	case "size", "mod":
		return s
	}
	return "name"
}

// sortEntries 原地排序：目录恒在前，其余按指定字段排。
//
// 输入：entries 目录项；key name/size/mod；desc 是否倒序
// 输出：无（原地排序）
// 预期：倒序也不把目录混进文件里
func sortEntries(entries []fm.Entry, key string, desc bool) {
	less := func(a, b fm.Entry) bool {
		switch key {
		case "size":
			if a.Size != b.Size {
				return a.Size < b.Size
			}
		case "mod":
			if !a.Mod.Equal(b.Mod) {
				return a.Mod.Before(b.Mod)
			}
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	}
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if a.Dir != b.Dir {
			return a.Dir
		}
		if desc {
			return less(b, a)
		}
		return less(a, b)
	})
}

// downloadURL 拼下载地址。
//
// 输入：vol 卷名；p 卷内路径
// 输出：转义好的 URL
// 预期：
//   - 参数值走 url.Values 百分号编码，中文名和特殊字符都安全
//   - 包成 template.URL 避免模板二次转义 %2F；& 由 html/template 自己转成 &amp;
func downloadURL(vol, p string) template.URL {
	q := url.Values{}
	q.Set("vol", vol)
	q.Set("path", p)
	return template.URL("/files/download?" + q.Encode())
}

// playURL 拼内嵌播放页地址。
//
// 输入：vol 卷名；p 卷内路径
// 输出：转义好的 URL
// 预期：与下载地址同样的编码规则，服务端用 Range 流式读取
func playURL(vol, p string) template.URL {
	q := url.Values{}
	q.Set("vol", vol)
	q.Set("path", p)
	return template.URL("/frag/play?" + q.Encode())
}

// renderBrowser 输出浏览器片段。
//
// 输入：w；data
// 输出：无
// 预期：同时带回工具栏状态（卷、目录），后续操作不会丢上下文
func renderBrowser(w http.ResponseWriter, data browserData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := pageTmpl.ExecuteTemplate(w, "browser", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// crumbsOf 把 /a/b 拆成根 + 各级。
//
// 输入：目录路径
// 输出：面包屑；根名为 "/"
// 预期：路径非法时退化成只有根
func crumbsOf(dir string) []crumb {
	out := []crumb{{Name: "/", Path: "/"}}
	cur := ""
	for _, seg := range strings.Split(strings.Trim(dir, "/"), "/") {
		if seg == "" {
			continue
		}
		cur += "/" + seg
		out = append(out, crumb{Name: seg, Path: cur})
	}
	return out
}

// normalizeDir 规范化目录参数。
func normalizeDir(dir string) string {
	dir = strings.ReplaceAll(dir, `\`, "/")
	if dir == "" {
		return "/"
	}
	if !strings.HasPrefix(dir, "/") {
		dir = "/" + dir
	}
	return path.Clean(dir)
}

// safeName 校验用户给的文件名。
//
// 输入：原始名字
// 输出：去掉路径成分后合法的名字；非法返回空串
// 预期：不允许带目录分隔符或 ..，避免越出当前目录
func safeName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." {
		return ""
	}
	if strings.ContainsAny(name, `/\`) {
		return ""
	}
	return name
}

// fmError 把内部错误翻成人话。
func fmError(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, fm.ErrBadPath):
		return i18n.T("err.fm_bad_path")
	case errors.Is(err, fm.ErrExists):
		return i18n.T("err.fm_exists")
	case errors.Is(err, os.ErrNotExist):
		return i18n.T("err.fm_not_found")
	}
	return err.Error()
}

// formatMod 把修改时间格式化成表格里的一列。
func formatMod(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return t.Format("2006-01-02 15:04")
}
