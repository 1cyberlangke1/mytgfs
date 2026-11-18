package web

// 桌面面板与网页版共用的 JSON API。
//
// 契约（全部 camelCase）：
//
//	GET  /api/state            卷列表 + 连接状态 + 传输进度 + 设置摘要 + 挂盘状态
//	GET  /api/files?vol=&dir=  目录直接子项；带 all=1 时返回整卷条目
//	POST /api/files/mkdir      {vol,dir,name}
//	POST /api/files/move       {vol,paths[],to}
//	POST /api/files/remove     {vol,paths[]}
//	POST /api/files/upload     multipart: vol,dir,rel,file
//	GET  /api/file?vol=&path=  原文件（支持 Range，inline=1 用于内嵌预览）
//	GET  /api/thumb?vol=&path= 图片缩略图（ETag 缓存）
//	GET  /api/settings         结构化设置 + 原始配置文本
//	PUT  /api/settings         只写结构化键，rawConfigText 仅展示不回写
//	POST /api/sync             立即增量同步

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"mytgfs/internal/config"
	"mytgfs/internal/fm"
	"mytgfs/internal/i18n"
)

// filesAPI 汇总文件管理所需的全部依赖。
type filesAPI struct {
	manager func(volume string) (fm.Files, bool)
	volumes func() []string
	// stats 可选：返回卷内文件数与已用字节
	stats  func(volume string) (files, used int64, ok bool)
	thumbs *thumbCache
}

// newFilesAPI 构造文件 API；缺少卷或后端时返回 nil。
func newFilesAPI(manager func(volume string) (fm.Files, bool), volumes func() []string, stats func(volume string) (int64, int64, bool), thumbDir string) *filesAPI {
	if manager == nil || volumes == nil {
		return nil
	}
	return &filesAPI{manager: manager, volumes: volumes, stats: stats, thumbs: newThumbCache(thumbDir)}
}

// lookup 取指定卷的文件后端。
func (api *filesAPI) lookup(volume string) (fm.Files, bool) {
	if api == nil || volume == "" {
		return nil, false
	}
	return api.manager(volume)
}

// fileItem 是前端 FileItem 的序列化形态；id 直接用绝对路径。
type fileItem struct {
	ID           string  `json:"id"`
	VolumeID     string  `json:"volumeId"`
	ParentID     *string `json:"parentId"`
	Name         string  `json:"name"`
	IsDir        bool    `json:"isDir"`
	Size         int64   `json:"size"`
	UpdatedAt    string  `json:"updatedAt"`
	Category     string  `json:"category"`
	Path         string  `json:"path"`
	ThumbnailURL string  `json:"thumbnailUrl,omitempty"`
	PreviewURL   string  `json:"previewUrl,omitempty"`
}

// toFileItem 把条目转换成前端结构。
//
// 输入：vol 卷名；p 绝对路径；e 目录项
// 输出：FileItem；图片带缩略图与预览地址
// 预期：根目录下的条目 parentId 为 null
func toFileItem(vol, p string, e fm.Entry) fileItem {
	dir := path.Dir(p)
	var parent *string
	if dir != "/" && dir != "." {
		parent = &dir
	}
	name := path.Base(p)
	cat := inferCategory(name, e.Dir)
	it := fileItem{
		ID:        p,
		VolumeID:  vol,
		ParentID:  parent,
		Name:      name,
		IsDir:     e.Dir,
		Size:      e.Size,
		UpdatedAt: e.Mod.UTC().Format(time.RFC3339),
		Category:  cat,
		Path:      p,
	}
	if !e.Dir {
		switch cat {
		case "image":
			it.ThumbnailURL = "/api/thumb?" + queryEscape("vol", vol, "path", p)
		case "video":
			it.ThumbnailURL = "/api/vthumb?" + queryEscape(
				"vol", vol, "path", p, "v", it.UpdatedAt)
		}
	}
	if !e.Dir {
		it.PreviewURL = "/api/file?" + queryEscape("vol", vol, "path", p, "inline", "1")
	}
	return it
}

// inferCategory 按扩展名推断前端分类。
func inferCategory(name string, isDir bool) string {
	if isDir {
		return "folder"
	}
	switch strings.ToLower(path.Ext(name)) {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp", ".svg", ".avif", ".heic":
		return "image"
	case ".mp4", ".mkv", ".mov", ".webm", ".avi", ".m4v":
		return "video"
	case ".mp3", ".flac", ".wav", ".ogg", ".m4a", ".aac", ".opus":
		return "audio"
	case ".pdf":
		return "pdf"
	case ".zip", ".rar", ".7z", ".tar", ".gz", ".bz2", ".xz", ".zst":
		return "archive"
	default:
		return "document"
	}
}

// queryEscape 用 url.Values 语义拼查询串（空格转 +）。
func queryEscape(kv ...string) string {
	var b strings.Builder
	for i := 0; i+1 < len(kv); i += 2 {
		if b.Len() > 0 {
			b.WriteByte('&')
		}
		b.WriteString(kv[i])
		b.WriteByte('=')
		b.WriteString(urlQueryEscape(kv[i+1]))
	}
	return b.String()
}

// urlQueryEscape 只转义查询参数需要的字符。
func urlQueryEscape(s string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '_' || c == '.' || c == '~' || c == '/' {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hex[c>>4])
		b.WriteByte(hex[c&0xf])
	}
	return b.String()
}

// volumeState 是面板展示一卷所需的最小信息。
type volumeState struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Title 是频道标题（Telegram 侧显示名）；空表示读不到，面板回落到 Name
	Title   string `json:"title"`
	Entries int64  `json:"entries"`
	Used    int64  `json:"used"`
	// Cover 是频道头像版本（photo id）；空串表示没有头像
	Cover string `json:"cover"`
}

// handleAPIState 返回整页初始化需要的快照。
func handleAPIState(api *filesAPI, opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			replyJSONError(w, http.StatusMethodNotAllowed, i18n.T("err.web_method"))
			return
		}
		vols := make([]volumeState, 0, 4)
		if api != nil {
			for _, name := range api.volumes() {
				var files, used int64
				if api.stats != nil {
					files, used, _ = api.stats(name)
				}
				coverVer := ""
				title := ""
				if opts.Cover != nil {
					if v, cerr := opts.Cover.CoverVersion(r.Context(), name); cerr == nil {
						coverVer = v
					}
					// 标题读不到不算错误：面板回落到卷名，不能让整页状态失败
					if v, terr := opts.Cover.Title(r.Context(), name); terr == nil {
						title = v
					}
				}
				vols = append(vols, volumeState{
					ID:      name,
					Name:    name,
					Title:   title,
					Entries: files,
					Used:    used,
					Cover:   coverVer,
				})
			}
		}
		resp := map[string]any{"volumes": vols}
		// 首次安装（安卓全新包）还没有凭证：面板据此直接落到账号设置页，
		// 不能让用户先看到一堆空数据再自己找登录入口
		if opts.Auth != nil {
			st := opts.Auth.Status()
			resp["login_ready"] = st.APIID > 0 && st.APIHashSet
		}
		resp["caps"] = map[string]bool{
			"createVolume": opts.CreateVolume != nil,
			"deleteVolume": opts.DeleteVolume != nil,
			"attachVolume": opts.AttachVolume != nil,
			"listChannels": opts.Channels != nil,
			"login":        opts.Auth != nil,
			"autostart":    opts.Autostart != nil,
			"cover":        opts.Cover != nil,
			"rebuild":      opts.Rebuild != nil,
		}
		if opts.Status != nil {
			if st, err := opts.Status(r.Context()); err == nil {
				resp["status"] = st
			}
		}
		replyJSON(w, http.StatusOK, resp)
	}
}

// walkAll 递归收集整卷条目。
//
// 输入：ctx；m 文件后端；dir 起始目录；out 收集器；depth 当前深度
// 输出：truncated 是否因上限被截断；error
// 预期：深度上限 32、条目上限 50000，避免异常目录把内存打满；
// 命中上限时返回 truncated=true，调用方据此提示用户而不是装作完整
func walkAll(ctx context.Context, m fm.Files, vol, dir string, out *[]fileItem, depth int) (bool, error) {
	if depth > 32 {
		return true, nil
	}
	if len(*out) >= 50000 {
		return true, nil
	}
	entries, err := m.List(ctx, dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	for _, e := range entries {
		p := path.Join(dir, e.Name)
		*out = append(*out, toFileItem(vol, p, e))
		if e.Dir {
			trunc, err := walkAll(ctx, m, vol, p, out, depth+1)
			if err != nil {
				return trunc, err
			}
			if trunc {
				return true, nil
			}
		}
	}
	return false, nil
}

// handleAPIFiles 列目录或整卷。
func handleAPIFiles(api *filesAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			replyJSONError(w, http.StatusMethodNotAllowed, i18n.T("err.web_method"))
			return
		}
		q := r.URL.Query()
		vol := q.Get("vol")
		m, ok := api.lookup(vol)
		if !ok {
			replyJSONError(w, http.StatusNotFound, i18n.T("err.fm_no_volume"))
			return
		}
		if q.Get("all") == "1" {
			out := make([]fileItem, 0, 64)
			truncated, err := walkAll(r.Context(), m, vol, "/", &out, 0)
			if err != nil {
				replyJSONError(w, http.StatusBadGateway, fmError(err))
				return
			}
			replyJSON(w, http.StatusOK, map[string]any{"files": out, "truncated": truncated})
			return
		}
		dir := normalizeDir(q.Get("dir"))
		entries, err := m.List(r.Context(), dir)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				replyJSON(w, http.StatusNotFound, map[string]any{"files": []fileItem{}, "error": fmError(err)})
				return
			}
			replyJSONError(w, http.StatusBadGateway, fmError(err))
			return
		}
		out := make([]fileItem, 0, len(entries))
		for _, e := range entries {
			out = append(out, toFileItem(vol, path.Join(dir, e.Name), e))
		}
		replyJSON(w, http.StatusOK, map[string]any{"files": out, "dir": dir})
	}
}

// jsonBody 是写操作的公共入参。
//
// Dest 是改名/移动时的完整目标路径（例如 /a/b.txt），前端在改名时必填；
// 批量移动只给 To（目标目录），此时目标文件名沿用原名。
type jsonBody struct {
	Vol   string   `json:"vol"`
	Dir   string   `json:"dir"`
	Name  string   `json:"name"`
	To    string   `json:"to"`
	Dest  string   `json:"dest"`
	Paths []string `json:"paths"`
}

// pathFailure 是批量操作里单条的失败原因。
//
// 输入：Path 卷内路径；Error 人话原因
// 输出：JSON {"path","error"}
// 预期：让前端能按条展示「哪几个成功、哪几个失败」
type pathFailure struct {
	Path  string `json:"path"`
	Error string `json:"error"`
}

// decodeBody 解析 JSON 请求体。
func decodeBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(dst); err != nil {
		replyJSONError(w, http.StatusBadRequest, i18n.T("err.web_bad_json"))
		return false
	}
	return true
}

// requirePost 校验写接口的请求方法。
//
// 输入：w/r 当前请求
// 输出：true 表示方法正确；false 表示已回 405，调用方立即 return
// 预期：所有 JSON 写接口共用，免得每个 handler 抄一遍同样的四行
func requirePost(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodPost {
		return true
	}
	replyJSONError(w, http.StatusMethodNotAllowed, i18n.T("err.web_method"))
	return false
}

// handleAPIMkdir 新建目录。
func handleAPIMkdir(api *filesAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requirePost(w, r) {
			return
		}
		var body jsonBody
		if !decodeBody(w, r, &body) {
			return
		}
		m, ok := api.lookup(body.Vol)
		if !ok {
			replyJSONError(w, http.StatusNotFound, i18n.T("err.fm_no_volume"))
			return
		}
		name := safeName(body.Name)
		if name == "" {
			replyJSONError(w, http.StatusBadRequest, i18n.T("err.fm_bad_path"))
			return
		}
		target := path.Join(normalizeDir(body.Dir), name)
		// 已存在同名目录按成功返回：导入流程会对每个目录先建一次（空目录也要
		// 在远端存在），重复导入同一棵树不该被当成失败。
		if err := m.Mkdir(r.Context(), target); err != nil && !errors.Is(err, fm.ErrExists) {
			replyJSONError(w, http.StatusBadGateway, fmError(err))
			return
		}
		replyJSON(w, http.StatusOK, map[string]any{"path": target})
	}
}

// handleAPIMove 移动/改名一个或多个条目。
func handleAPIMove(api *filesAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requirePost(w, r) {
			return
		}
		var body jsonBody
		if !decodeBody(w, r, &body) {
			return
		}
		m, ok := api.lookup(body.Vol)
		if !ok {
			replyJSONError(w, http.StatusNotFound, i18n.T("err.fm_no_volume"))
			return
		}
		if len(body.Paths) == 0 || body.To == "" {
			replyJSONError(w, http.StatusBadRequest, i18n.T("err.fm_bad_path"))
			return
		}
		// 单条给了 dest 就是改名（可能同时换目录）：名字必须按 dest 走，
		// 否则 path.Base(src) 会把新名字丢掉、只做同目录无操作
		dest := ""
		if len(body.Paths) == 1 && body.Dest != "" {
			raw := strings.TrimSpace(body.Dest)
			// 先查原始串里的 ..：path.Clean 会把 /../x 折成 /x，越界就看不出来了
			if !strings.HasPrefix(raw, "/") || strings.Contains(raw, "..") {
				replyJSONError(w, http.StatusBadRequest, i18n.T("err.fm_bad_path"))
				return
			}
			dest = path.Clean(raw)
			if dest == "/" {
				replyJSONError(w, http.StatusBadRequest, i18n.T("err.fm_bad_path"))
				return
			}
		}
		failed := make([]pathFailure, 0)
		moved := 0
		for _, src := range body.Paths {
			if strings.Contains(src, "..") {
				replyJSONError(w, http.StatusBadRequest, i18n.T("err.fm_bad_path"))
				return
			}
			dst := path.Join(normalizeDir(body.To), path.Base(src))
			if dest != "" {
				dst = dest
			}
			if err := m.Move(r.Context(), src, dst); err != nil {
				// 批量操作逐条回报：前面成功的已经改了远端，
				// 整批 502 会让用户不知道哪些生效、重试还会重复移动
				failed = append(failed, pathFailure{Path: src, Error: fmError(err)})
				continue
			}
			moved++
			// 旧路径的缩略图/封面缓存随改名/移动一起清掉
			api.thumbs.purgeEntry(body.Vol, src)
		}
		status := http.StatusOK
		if moved == 0 && len(failed) > 0 {
			status = http.StatusBadGateway
		}
		replyJSON(w, status, map[string]any{"moved": moved, "failed": failed})
	}
}

// handleAPIRemove 删除一个或多个条目。
func handleAPIRemove(api *filesAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requirePost(w, r) {
			return
		}
		var body jsonBody
		if !decodeBody(w, r, &body) {
			return
		}
		m, ok := api.lookup(body.Vol)
		if !ok {
			replyJSONError(w, http.StatusNotFound, i18n.T("err.fm_no_volume"))
			return
		}
		if len(body.Paths) == 0 {
			replyJSONError(w, http.StatusBadRequest, i18n.T("err.fm_bad_path"))
			return
		}
		failed := make([]pathFailure, 0)
		removed := 0
		for _, p := range body.Paths {
			if strings.Contains(p, "..") {
				replyJSONError(w, http.StatusBadRequest, i18n.T("err.fm_bad_path"))
				return
			}
			if err := m.Remove(r.Context(), p); err != nil && !errors.Is(err, os.ErrNotExist) {
				// 逐条回报，理由同批量移动：已删的不可回滚
				failed = append(failed, pathFailure{Path: p, Error: fmError(err)})
				continue
			}
			removed++
			// 文件没了，缩略图/封面缓存也一起清掉，不留孤儿
			api.thumbs.purgeEntry(body.Vol, p)
		}
		status := http.StatusOK
		if removed == 0 && len(failed) > 0 {
			status = http.StatusBadGateway
		}
		replyJSON(w, status, map[string]any{"removed": removed, "failed": failed})
	}
}

// handleAPIUpload 已移除：桌面壳的 AssetServer 无法可靠传递 multipart 请求体
// （Wails issue #5845/#6068），前端改用 /api/upload/init|chunk|commit 发送原始字节。

// handleAPIFile 输出原文件，支持 Range 与 inline。
func handleAPIFile(api *filesAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, i18n.T("err.web_method"), http.StatusMethodNotAllowed)
			return
		}
		q := r.URL.Query()
		m, ok := api.lookup(q.Get("vol"))
		if !ok {
			http.NotFound(w, r)
			return
		}
		p := q.Get("path")
		if p == "" || strings.Contains(p, "..") {
			http.Error(w, i18n.T("err.fm_bad_path"), http.StatusBadRequest)
			return
		}
		f, err := m.Open(r.Context(), p)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer f.Close()
		// inline=1 是内嵌预览/播放，不是下载任务；标记类型让前端操作队列跳过它
		if q.Get("inline") == "1" {
			if s, ok := f.(interface{ MarkStream() }); ok {
				s.MarkStream()
			}
		}
		st, err := f.Stat()
		if err != nil {
			// 底层错误里可能带内部路径，只给通用提示
			http.Error(w, i18n.T("err.http_bad_gateway"), http.StatusBadGateway)
			return
		}
		disp := "attachment"
		if q.Get("inline") == "1" {
			disp = "inline"
		}
		w.Header().Set("Content-Disposition", disp+"; filename*=UTF-8''"+urlQueryEscape(path.Base(p)))
		mime := mimeOf(path.Base(p))
		w.Header().Set("Content-Type", mime)
		// inline 会原样渲染 SVG：没有 nosniff 时浏览器可能按 HTML 解析，
		// 同源下等于给上传的 SVG 一个执行脚本的机会
		w.Header().Set("X-Content-Type-Options", "nosniff")
		// 桌面壳会把一个响应的正文整份缓冲；媒体只给一个窗口，
		// 浏览器播到窗口末尾会自己再发 Range，于是内存有界且可拖动。
		// 只在客户端确实发了 Range 时窗口化：安卓壳只转发 200 的正文，
		// 把无 Range 的整份请求改成 206 会让它拿到空体。
		if isStreamMime(mime) && st.Size() > mediaWindow && r.Header.Get("Range") != "" {
			start, end := rangeWindow(r.Header.Get("Range"), st.Size())
			if end-start+1 > mediaWindow {
				end = start + mediaWindow - 1
			}
			r.Header.Set("Range", "bytes="+strconv.FormatInt(start, 10)+"-"+strconv.FormatInt(end, 10))
		}
		http.ServeContent(w, r, path.Base(p), st.ModTime(), f)
	}
}

// mediaWindow 是单次媒体响应的字节上限（桌面壳每个响应会整份缓冲）。
const mediaWindow = 1 << 20

// isStreamMime 判断是否是需要分段拉取的媒体类型。
//
// 输入：MIME
// 输出：video/* 或 audio/* 为 true
// 预期：图片/文档不窗口化，避免把多请求开销加到小文件上
func isStreamMime(mime string) bool {
	return strings.HasPrefix(mime, "video/") || strings.HasPrefix(mime, "audio/")
}

// rangeWindow 解析单段 Range，返回闭区间 [start,end]。
//
// 输入：Range 头（可为空）；文件总长
// 输出：起止偏移，已夹在文件范围内
// 预期：只支持单段；无法解析时按「从头开始」处理，交给上层去截窗口
func rangeWindow(header string, size int64) (int64, int64) {
	if size <= 0 {
		return 0, 0
	}
	start, end := int64(0), size-1
	spec, ok := strings.CutPrefix(header, "bytes=")
	if !ok {
		return start, end
	}
	spec = strings.TrimSpace(strings.SplitN(spec, ",", 2)[0])
	a, b, found := strings.Cut(spec, "-")
	if !found {
		return start, end
	}
	if a == "" {
		// 后缀形式 bytes=-N：最后 N 字节
		n, err := strconv.ParseInt(strings.TrimSpace(b), 10, 64)
		if err != nil || n <= 0 {
			return start, end
		}
		if n > size {
			n = size
		}
		return size - n, size - 1
	}
	parsed, err := strconv.ParseInt(strings.TrimSpace(a), 10, 64)
	if err != nil || parsed < 0 {
		return start, end
	}
	if parsed >= size {
		// 起点已越过文件尾：返回空区间（end 小于 start），
		// 上层拼成 "bytes=<size>-<size-1>" 后由 ServeContent 回 416
		return size, size - 1
	}
	start = parsed
	if b != "" {
		if v, err := strconv.ParseInt(strings.TrimSpace(b), 10, 64); err == nil && v >= start {
			end = min(v, size-1)
		}
	}
	return start, end
}

// handleAPIFileWindow 是给安卓壳用的内部窗口读取。
//
// 输入：GET ?vol=&path=&offset=&length=
// 输出：200 + 恰好 length 字节；参数非法 400；越界 416
// 预期：这不是 HTTP Range 语义，只是「读一段」的 RPC；
// 对 WebView 的 206/Content-Range 由壳自己组装
func handleAPIFileWindow(api *filesAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, i18n.T("err.web_method"), http.StatusMethodNotAllowed)
			return
		}
		q := r.URL.Query()
		m, ok := api.lookup(q.Get("vol"))
		if !ok {
			http.NotFound(w, r)
			return
		}
		p := q.Get("path")
		if p == "" || strings.Contains(p, "..") {
			http.Error(w, i18n.T("err.fm_bad_path"), http.StatusBadRequest)
			return
		}
		offset, offErr := strconv.ParseInt(q.Get("offset"), 10, 64)
		length, lenErr := strconv.ParseInt(q.Get("length"), 10, 64)
		if offErr != nil || lenErr != nil || offset < 0 || length <= 0 || length > 8<<20 {
			http.Error(w, i18n.T("err.web_bad_request"), http.StatusBadRequest)
			return
		}
		f, err := m.Open(r.Context(), p)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer f.Close()
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			http.Error(w, i18n.T("err.http_bad_request"), http.StatusRequestedRangeNotSatisfiable)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", strconv.FormatInt(length, 10))
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.Method == http.MethodHead {
			return
		}
		written, copyErr := io.CopyN(w, f, length)
		if copyErr != nil && written == 0 {
			http.Error(w, i18n.T("err.http_bad_gateway"), http.StatusBadGateway)
		}
	}
}

// handleAPIFileSize 返回文件字节数（安卓壳用来组装 Content-Range）。
//
// 输入：GET ?vol=&path=
// 输出：{"size":N}；目录/不存在 404
// 预期：只读元数据，不打开文件内容
func handleAPIFileSize(api *filesAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, i18n.T("err.web_method"), http.StatusMethodNotAllowed)
			return
		}
		q := r.URL.Query()
		m, ok := api.lookup(q.Get("vol"))
		if !ok {
			http.NotFound(w, r)
			return
		}
		p := q.Get("path")
		if p == "" || strings.Contains(p, "..") {
			http.Error(w, i18n.T("err.fm_bad_path"), http.StatusBadRequest)
			return
		}
		info, err := m.Info(r.Context(), p)
		if err != nil || info.Dir {
			http.NotFound(w, r)
			return
		}
		replyJSON(w, http.StatusOK, map[string]any{"size": info.Size})
	}
}

// handleAPISettings 读写结构化设置。
//
// 输入：GET 无参；PUT {"values":{"web.port":8720,...}}
// 输出：GET {"schema":[{"key","kind"}],"values":{...},"rawConfigText":"..."}
// 预期：
//   - schema 直接来自 config.Schema()，面板不需要手写键表
//   - 缺省键不返回；PUT 只改提交的键，未知键返回 400
//   - PUT 经 opts.MutateConfig 收敛：写盘与内存副本一步完成；
//     成功后触发 opts.OnSettingsChanged，让并发数、日志级别、下载目录立即生效
func handleAPISettings(configPath string, opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			raw, err := os.ReadFile(configPath)
			if err != nil && !os.IsNotExist(err) {
				replyJSONError(w, http.StatusInternalServerError, i18n.T("err.config_read"))
				return
			}
			schema := config.Schema()
			values := make(map[string]string, len(schema))
			for _, s := range schema {
				v, ok, err := config.GetKey(configPath, s.Key)
				if err != nil {
					continue
				}
				if !ok {
					// 缺省键给真实默认值，面板显示 15/8/4 而不是 0
					v = config.DefaultValue(s.Key)
				}
				values[s.Key] = v
			}
			replyJSON(w, http.StatusOK, map[string]any{
				"schema":        schema,
				"values":        values,
				"rawConfigText": string(raw),
			})
		case http.MethodPut:
			var in struct {
				Values map[string]any `json:"values"`
			}
			if !decodeBody(w, r, &in) {
				return
			}
			// 走唯一写入口：写盘与内存副本同步，避免 App 按旧配置继续跑
			mutate := func(p string) error {
				for k, v := range in.Values {
					if err := config.SetKey(p, k, valueString(v)); err != nil {
						return err
					}
				}
				return nil
			}
			if opts.MutateConfig != nil {
				if err := opts.MutateConfig(mutate); err != nil {
					replyJSONError(w, http.StatusBadRequest, err.Error())
					return
				}
			} else if err := mutate(configPath); err != nil {
				replyJSONError(w, http.StatusBadRequest, err.Error())
				return
			}
			// 现读最新配置广播出去：装配根借此即时应用并发数/日志级别并推送
			if opts.OnSettingsChanged != nil {
				if cur, err := config.Load(configPath); err == nil {
					opts.OnSettingsChanged(cur)
				}
			}
			replyJSON(w, http.StatusOK, map[string]any{"saved": true})
		default:
			replyJSONError(w, http.StatusMethodNotAllowed, i18n.T("err.web_method"))
		}
	}
}

// valueString 把 JSON 值转成配置写入需要的字符串。
//
// 输入：任意 JSON 标量（string/bool/float64）
// 输出：字符串形式；float 整数不带小数点
// 预期：供 config.SetKey 再按类型校验，避免界面传 1 变成 "1.000000"
func valueString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case nil:
		return ""
	default:
		return fmt.Sprint(x)
	}
}

// handleAPISync 触发一次增量同步。
func handleAPISync(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requirePost(w, r) {
			return
		}
		if opts.Sync == nil {
			replyJSONError(w, http.StatusNotImplemented, i18n.T("web.no_instance"))
			return
		}
		res, err := opts.Sync(r.Context())
		if err != nil {
			replyJSONError(w, http.StatusBadGateway, err.Error())
			return
		}
		replyJSON(w, http.StatusOK, res)
	}
}

// mimeOf 按扩展名给出常见 MIME；未知回退二进制流。
func mimeOf(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".svg":
		return "image/svg+xml"
	case ".avif":
		return "image/avif"
	case ".mp4", ".m4v":
		return "video/mp4"
	case ".webm":
		return "video/webm"
	case ".mov":
		return "video/quicktime"
	case ".mkv":
		return "video/x-matroska"
	case ".mp3":
		return "audio/mpeg"
	case ".flac":
		return "audio/flac"
	case ".wav":
		return "audio/wav"
	case ".ogg", ".opus":
		return "audio/ogg"
	case ".m4a", ".aac":
		return "audio/mp4"
	case ".pdf":
		return "application/pdf"
	case ".txt", ".md", ".log":
		return "text/plain; charset=utf-8"
	case ".json":
		return "application/json"
	default:
		return "application/octet-stream"
	}
}

// replyJSON 输出 JSON 响应。
func replyJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// opFailed 把「本地化的操作名 + 上游原因」拼成一句人话。
//
// 输入：prefixKey 是描述这次操作的 i18n 键；err 是上游返回的错误
// 输出：形如「创建卷失败: rpc error ...」的字符串；err 为 nil 时返回空串
// 预期：前缀必定本地化，前端不会拿到只有原始英文 RPC 文本的错误；
// 与 CLI 的 failErr 口径一致，两个入口说的是同一句话
func opFailed(prefixKey string, err error) string {
	if err == nil {
		return ""
	}
	return i18n.T(prefixKey) + ": " + err.Error()
}

// replyJSONError 输出 {error:{message}} 形态的错误。
func replyJSONError(w http.ResponseWriter, code int, msg string) {
	replyJSON(w, code, map[string]any{"error": map[string]string{"message": msg}})
}
