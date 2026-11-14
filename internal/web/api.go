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
	"sync"
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

	// coverMu/coverCache 缓存「这卷有没有封面」，避免每次状态轮询都列目录
	coverMu    sync.Mutex
	coverCache map[string]coverEntry
}

// coverEntry 是封面探测结果的短缓存。
type coverEntry struct {
	at  time.Time
	has bool
}

// coverTTL 是封面探测缓存时长：改封面后最多这么久生效，足够短。
const coverTTL = 15 * time.Second

// newFilesAPI 构造文件 API；缺少卷或后端时返回 nil。
func newFilesAPI(manager func(volume string) (fm.Files, bool), volumes func() []string, stats func(volume string) (int64, int64, bool), thumbDir string) *filesAPI {
	if manager == nil || volumes == nil {
		return nil
	}
	return &filesAPI{manager: manager, volumes: volumes, stats: stats, thumbs: newThumbCache(thumbDir)}
}

// dropCover 让某卷的封面探测缓存立即失效（上传/删除封面后调用）。
func (api *filesAPI) dropCover(volume string) {
	if api == nil {
		return
	}
	api.coverMu.Lock()
	delete(api.coverCache, volume)
	api.coverMu.Unlock()
}

// hasCover 判断卷根目录是否有保留封面文件，带 15s 缓存。
//
// 输入：ctx；volume 卷名
// 输出：true 表示存在封面
// 预期：供 /api/state 告诉前端要不要请求封面图，避免每卷一个 404
func (api *filesAPI) hasCover(ctx context.Context, volume string) bool {
	if api == nil {
		return false
	}
	api.coverMu.Lock()
	ent, ok := api.coverCache[volume]
	api.coverMu.Unlock()
	if ok && time.Since(ent.at) < coverTTL {
		return ent.has
	}
	has := false
	if m, ok := api.manager(volume); ok {
		if _, found := findCover(ctx, m); found {
			has = true
		}
	}
	api.coverMu.Lock()
	if api.coverCache == nil {
		api.coverCache = make(map[string]coverEntry)
	}
	api.coverCache[volume] = coverEntry{at: time.Now(), has: has}
	api.coverMu.Unlock()
	return has
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
	if !e.Dir && cat == "image" {
		it.ThumbnailURL = "/api/thumb?" + queryEscape("vol", vol, "path", p)
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

// volumeState 是前端需要的卷摘要。
// volumeState 是面板展示一卷所需的最小信息。
type volumeState struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Entries int64  `json:"entries"`
	Used    int64  `json:"used"`
	Cover   bool   `json:"cover"`
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
				vols = append(vols, volumeState{
					ID:      name,
					Name:    name,
					Entries: files,
					Used:    used,
					Cover:   api.hasCover(r.Context(), name),
				})
			}
		}
		resp := map[string]any{"volumes": vols}
		resp["caps"] = map[string]bool{
			"createVolume": opts.CreateVolume != nil,
			"deleteVolume": opts.DeleteVolume != nil,
			"attachVolume": opts.AttachVolume != nil,
			"listChannels": opts.Channels != nil,
			"login":        opts.Auth != nil,
			"autostart":    opts.Autostart != nil,
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
// 输出：error；条目追加到 out
// 预期：深度上限 32、条目上限 50000，避免异常目录把内存打满
func walkAll(ctx context.Context, m fm.Files, vol, dir string, out *[]fileItem, depth int) error {
	if depth > 32 || len(*out) >= 50000 {
		return nil
	}
	entries, err := m.List(ctx, dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		p := path.Join(dir, e.Name)
		*out = append(*out, toFileItem(vol, p, e))
		if e.Dir {
			if err := walkAll(ctx, m, vol, p, out, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
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
			if err := walkAll(r.Context(), m, vol, "/", &out, 0); err != nil {
				replyJSONError(w, http.StatusBadGateway, fmError(err))
				return
			}
			replyJSON(w, http.StatusOK, map[string]any{"files": out})
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

// decodeBody 解析 JSON 请求体。
func decodeBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(dst); err != nil {
		replyJSONError(w, http.StatusBadRequest, i18n.T("err.web_bad_json"))
		return false
	}
	return true
}

// handleAPIMkdir 新建目录。
func handleAPIMkdir(api *filesAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			replyJSONError(w, http.StatusMethodNotAllowed, i18n.T("err.web_method"))
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
		if err := m.Mkdir(r.Context(), target); err != nil {
			replyJSONError(w, http.StatusBadGateway, fmError(err))
			return
		}
		replyJSON(w, http.StatusOK, map[string]any{"path": target})
	}
}

// handleAPIMove 移动/改名一个或多个条目。
func handleAPIMove(api *filesAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			replyJSONError(w, http.StatusMethodNotAllowed, i18n.T("err.web_method"))
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
				replyJSONError(w, http.StatusBadGateway, fmError(err))
				return
			}
		}
		replyJSON(w, http.StatusOK, map[string]any{"moved": len(body.Paths)})
	}
}

// handleAPIRemove 删除一个或多个条目。
func handleAPIRemove(api *filesAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			replyJSONError(w, http.StatusMethodNotAllowed, i18n.T("err.web_method"))
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
		for _, p := range body.Paths {
			if strings.Contains(p, "..") {
				replyJSONError(w, http.StatusBadRequest, i18n.T("err.fm_bad_path"))
				return
			}
			if err := m.Remove(r.Context(), p); err != nil && !errors.Is(err, os.ErrNotExist) {
				replyJSONError(w, http.StatusBadGateway, fmError(err))
				return
			}
		}
		api.dropCover(body.Vol)
		replyJSON(w, http.StatusOK, map[string]any{"removed": len(body.Paths)})
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
		st, err := f.Stat()
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		disp := "attachment"
		if q.Get("inline") == "1" {
			disp = "inline"
		}
		w.Header().Set("Content-Disposition", disp+"; filename*=UTF-8''"+urlQueryEscape(path.Base(p)))
		w.Header().Set("Content-Type", mimeOf(path.Base(p)))
		http.ServeContent(w, r, path.Base(p), st.ModTime(), f)
	}
}

// handleAPISettings 读写结构化设置。
//
// 输入：GET 无参；PUT {"values":{"web.port":8720,...}}
// 输出：GET {"schema":[{"key","kind"}],"values":{...},"rawConfigText":"..."}
// 预期：
//   - schema 直接来自 config.Schema()，面板不需要手写键表
//   - 缺省键不返回；PUT 只改提交的键，未知键返回 400
func handleAPISettings(configPath string) http.HandlerFunc {
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
			for k, v := range in.Values {
				if err := config.SetKey(configPath, k, valueString(v)); err != nil {
					replyJSONError(w, http.StatusBadRequest, err.Error())
					return
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
		if r.Method != http.MethodPost {
			replyJSONError(w, http.StatusMethodNotAllowed, i18n.T("err.web_method"))
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

// replyJSONError 输出 {error:{message}} 形态的错误。
func replyJSONError(w http.ResponseWriter, code int, msg string) {
	replyJSON(w, code, map[string]any{"error": map[string]string{"message": msg}})
}
