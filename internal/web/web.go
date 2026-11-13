package web

// 本地 Web 面板：查看连接/卷状态、触发增量同步、编辑配置。
//
// 只绑定 127.0.0.1；页面是嵌进二进制的静态前端，不依赖外网 CDN。
// 文案全部走 i18n，服务端渲染时注入语言包。

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/natefinch/atomic"
	"mytgfs/internal/config"
	"mytgfs/internal/eventbus"
	"mytgfs/internal/fm"
	"mytgfs/internal/i18n"
	"mytgfs/internal/version"
)

// tmplFuncs 是模板里可用的辅助函数。
var tmplFuncs = template.FuncMap{
	"bytes":   formatBytes,
	"fmtTime": formatMod,
	"urlq":    url.QueryEscape,
}

// formatBytes 把字节数格式化成人类可读的字符串。
//
// 输入：n 字节数
// 输出：如 "0 B"、"12.3 MB"
// 预期：保留一位小数（字节级除外），与旧前端显示风格一致
func formatBytes(n int64) string {
	units := []string{"B", "KB", "MB", "GB", "TB"}
	f := float64(n)
	i := 0
	for f >= 1024 && i < len(units)-1 {
		f /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%d %s", n, units[i])
	}
	return fmt.Sprintf("%.1f %s", f, units[i])
}

// StatusFunc 返回面板要展示的状态；nil 时只有空状态。
type StatusFunc func(ctx context.Context) (any, error)

// SyncFunc 立即触发一次增量同步并返回结果。
type SyncFunc func(ctx context.Context) (any, error)

// CreateVolumeFunc 创建新卷并返回可序列化的结果。
//
// 输入：ctx；name 卷名；title 频道标题；point 挂载点
// 输出：结果对象；error
// 预期：重名返回 config.ErrVolumeExists，供 handler 映射成 409
type CreateVolumeFunc func(ctx context.Context, name, title, point string) (any, error)

// Options 是面板的可选能力，零值表示只提供配置编辑。
type Options struct {
	Status StatusFunc
	Sync   SyncFunc
	// CreateVolume 为 nil 时建卷接口返回 501，面板按钮置灰
	CreateVolume CreateVolumeFunc
	// Files 返回指定卷的文件管理后端；nil 表示不提供文件管理
	Files func(volume string) (fm.Files, bool)
	// Volumes 返回可管理卷名，按配置顺序
	Volumes func() []string
	// Stats 返回卷内文件数与已用字节；nil 时卷标签不显示用量
	Stats func(volume string) (files, used int64, ok bool)
	// TempDir 是上传临时文件目录；空则 data/tmp
	TempDir string
	// Mount 控制 WebDAV 挂盘开关；nil 表示不支持
	Mount *MountControl
	// Bus 用于把配置/挂载变更推给所有页面；nil 时 /events 返回 501
	Bus *eventbus.Bus
	// ThumbDir 是缩略图缓存目录；空则 data/thumbs
	ThumbDir string
	// PWA 控制可安装/离线能力；零值表示启用（默认好用）
	PWA config.PWA
}

// ServeWith 启动面板，阻塞到 ctx 取消。
//
// 输入：ctx；addr 监听地址；configPath 配置文件；opts 状态/同步钩子
// 输出：error
// 预期：ctx 取消时优雅关闭；opts.Status 为 nil 时状态为空列表
// NewHandler 返回面板的完整路由，供桌面壳直接挂在 webview 上。
//
// 输入：configPath 配置文件路径；opts 面板能力
// 输出：http.Handler
// 预期：与 ServeWith 使用同一套路由，不监听任何端口
func NewHandler(configPath string, opts Options) http.Handler {
	return newMux(configPath, opts)
}

// ServeWith 在 addr 上提供面板，ctx 取消时优雅关闭。
func ServeWith(ctx context.Context, addr, configPath string, opts Options) error {
	srv := &http.Server{Addr: addr, Handler: newMux(configPath, opts)}
	// AfterFunc 在 ctx 取消时优雅关闭，不再常驻等待 goroutine
	context.AfterFunc(ctx, func() {
		_ = srv.Shutdown(context.Background())
	})
	err := srv.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// newMux 组装面板的全部路由，拆出来供单测直接验证路由可达性。
//
// 输入：configPath 配置文件路径；opts 面板能力
// 输出：*http.ServeMux
// 预期：PWA 关闭时 manifest/sw/图标路径显式 404，不落到整页兜底
func newMux(configPath string, opts Options) *http.ServeMux {
	mux := http.NewServeMux()
	api := newFilesAPI(opts.Files, opts.Volumes, opts.Stats, opts.ThumbDir)
	api.setBus(opts.Bus)
	pwa := newPWAData(opts.PWA)
	mux.HandleFunc("/", handlePage(opts.CreateVolume != nil, opts.Sync != nil, configPath, api, opts.Mount, pwa))
	// htmx 与面板脚本随二进制分发：不依赖任何外网 CDN
	mux.HandleFunc("/static/htmx.min.js", handleAsset("application/javascript; charset=utf-8", htmxJS))
	mux.HandleFunc("/static/app.js", handleAsset("application/javascript; charset=utf-8", appJS))
	// PWA：启用时挂真实 handler；关闭时挂 404，避免被 "/" 兜底成整页 HTML
	pwaPaths := []string{"/manifest.webmanifest", "/sw.js",
		"/static/icon-192.png", "/static/icon-512.png", "/static/apple-touch-icon.png",
		"/favicon.ico"}
	if pwa.Enabled {
		mux.HandleFunc("/manifest.webmanifest", handleManifest(opts.PWA))
		mux.HandleFunc("/sw.js", handleServiceWorker())
		mux.HandleFunc("/static/icon-192.png", handleAsset("image/png", icon192))
		mux.HandleFunc("/static/icon-512.png", handleAsset("image/png", icon512))
		mux.HandleFunc("/static/apple-touch-icon.png", handleAsset("image/png", appleTouchIcon))
		// 浏览器会无条件请求 /favicon.ico，复用 192 图标避免 404 噪音
		mux.HandleFunc("/favicon.ico", handleAsset("image/png", icon192))
	} else {
		for _, p := range pwaPaths {
			mux.HandleFunc(p, func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
		}
	}
	// 片段接口：htmx 轮询/提交的目标，返回 HTML 而不是 JSON
	mux.HandleFunc("/frag/status", handleFragStatus(opts.Status))
	mux.HandleFunc("/frag/sync", handleFragSync(opts.Sync))
	mux.HandleFunc("/frag/config", handleFragConfig(configPath, opts.Bus))
	mux.HandleFunc("/frag/volume", handleFragVolume(opts.CreateVolume, configPath))
	mux.HandleFunc("/frag/mount", handleFragMount(opts.Mount, opts.Bus))
	// 事件流：配置/挂载变更主动推送
	mux.HandleFunc("/events", handleEvents(opts.Bus))
	// 文件管理
	mux.HandleFunc("/frag/files", handleFragFiles(api))
	mux.HandleFunc("/frag/files/upload", handleFragFilesUpload(api))
	mux.HandleFunc("/frag/files/mkdir", handleFragFilesMkdir(api))
	mux.HandleFunc("/frag/files/remove", handleFragFilesRemove(api))
	mux.HandleFunc("/frag/files/move", handleFragFilesMove(api))
	mux.HandleFunc("/frag/files/batch", handleFragFilesBatch(api))
	mux.HandleFunc("/files/download", handleFileDownload(api))
	mux.HandleFunc("/files/thumb", handleFileThumb(api))
	mux.HandleFunc("/frag/details", handleFragDetails(api, pwa))
	mux.HandleFunc("/frag/play", handleFragPlay(api))
	// 浏览器分片上传：大文件断线只重传当前分片
	if up := newUploadAPI(opts.Files, opts.TempDir); up != nil {
		up.bus = opts.Bus
		mux.HandleFunc("/api/upload/init", up.handleInit)
		mux.HandleFunc("/api/upload/chunk", up.handleChunk)
		mux.HandleFunc("/api/upload/commit", up.handleCommit)
		mux.HandleFunc("/api/upload", func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				up.handleStatus(w, r)
			case http.MethodDelete:
				up.handleAbort(w, r)
			default:
				w.WriteHeader(http.StatusMethodNotAllowed)
			}
		})
	}
	mux.HandleFunc("/api/status", handleStatus(opts.Status))
	mux.HandleFunc("/api/sync", handleSync(opts.Sync))
	mux.HandleFunc("/api/volumes", handleVolumes(opts.CreateVolume))
	mux.HandleFunc("/api/config", handleConfig(configPath))
	return mux
}

// pageLabels 是页面全部文案，统一取自 i18n。
//
// 模板里不出现任何硬编码的用户可见字符串，换语言只改语言包。
type pageLabels struct {
	Title, Subtitle, Volumes, Actions                      string
	Sync, Syncing, SyncDone                                string
	Config, Save, Saved                                    string
	Entries, Used, NoInstance, LoadError                   string
	NewVolume, FieldName, FieldTitle, FieldPoint           string
	Create, Creating, Created, Restart, AuthLost, NameNeed string
	StatVols, StatFiles, StatUsed, Xfer, UploadQueue       string
	Footer, Lang, ThemeTip, RefreshTip                     string
	// 文件管理
	Files, FilesUpload, FilesUploading, FilesMkdir, FilesNewDir string
	FilesName, FilesSize, FilesMtime, FilesEmpty                string
	FilesDownload, FilesMove, FilesDelete, FilesConfirm         string
	FilesDone, NetError                                         string
	FilesDetails, FilesPath                                     string
	FilesPrev, FilesNext                                        string
	// 搜索 / 排序 / 播放 / 批量
	FilesSearch, FilesSortName, FilesSortSize, FilesSortMod string
	FilesSortDesc, FilesPlay, FilesClose, FilesSelectAll    string
	FilesBatchDelete, FilesBatchMoveTo, FilesBatchGo        string
	FilesDrop, FilesDirUpload                               string
	// 离线（PWA）
	FilesOffline, FilesOfflineSave, FilesOfflineSaved         string
	FilesOfflineRemove, FilesOfflineRemoved, FilesOfflineFail string
	FilesOfflineLarge                                         string
	OfflineHint                                               string
	// CanCreate 决定建卷卡片是否渲染
	CanCreate bool
	// 挂载为网络驱动器
	Mount, MountOn, MountOff, MountHint string
	MountUnavailable, MountAddr         string
}

// loadLabels 读取当前语言的页面文案。
//
// 输入：canCreate 是否支持建卷
// 输出：文案集合
// 预期：语言由 i18n.Lang() 决定，与托盘/CLI 保持一致
func loadLabels(canCreate bool) pageLabels {
	return pageLabels{
		Title:       i18n.T("web.title"),
		Subtitle:    i18n.T("web.subtitle"),
		Volumes:     i18n.T("web.volumes"),
		Actions:     i18n.T("web.actions"),
		Sync:        i18n.T("web.sync"),
		Syncing:     i18n.T("web.syncing"),
		SyncDone:    i18n.T("web.sync_done"),
		Config:      i18n.T("web.config"),
		Save:        i18n.T("web.save"),
		Saved:       i18n.T("web.saved"),
		Entries:     i18n.T("web.entries"),
		Used:        i18n.T("web.used"),
		NoInstance:  i18n.T("web.no_instance"),
		LoadError:   i18n.T("web.load_error"),
		NewVolume:   i18n.T("web.new_volume"),
		FieldName:   i18n.T("web.field_name"),
		FieldTitle:  i18n.T("web.field_title"),
		FieldPoint:  i18n.T("web.field_point"),
		Create:      i18n.T("web.create"),
		Creating:    i18n.T("web.creating"),
		Created:     i18n.T("web.created"),
		Restart:     i18n.T("web.restart_hint"),
		AuthLost:    i18n.T("web.auth_lost"),
		NameNeed:    i18n.T("web.name_required"),
		StatVols:    i18n.T("web.stat_volumes"),
		StatFiles:   i18n.T("web.stat_files"),
		StatUsed:    i18n.T("web.stat_used"),
		Xfer:        i18n.T("web.xfer"),
		UploadQueue: i18n.T("web.upload_queue"),
		Footer:      i18n.T("web.footer"),
		Lang:        i18n.Lang(),
		ThemeTip:    i18n.T("web.theme_tip"),
		RefreshTip:  i18n.T("web.refresh_tip"),
		CanCreate:   canCreate,

		Files:          i18n.T("web.files"),
		FilesUpload:    i18n.T("web.files_upload"),
		FilesUploading: i18n.T("web.files_uploading"),
		FilesMkdir:     i18n.T("web.files_mkdir"),
		FilesNewDir:    i18n.T("web.files_newdir"),
		FilesName:      i18n.T("web.files_name"),
		FilesSize:      i18n.T("web.files_size"),
		FilesMtime:     i18n.T("web.files_mtime"),
		FilesEmpty:     i18n.T("web.files_empty"),
		FilesDownload:  i18n.T("web.files_download"),
		FilesMove:      i18n.T("web.files_move"),
		FilesDelete:    i18n.T("web.files_delete"),
		FilesConfirm:   i18n.T("web.files_confirm"),
		FilesDone:      i18n.T("web.files_done"),
		FilesDetails:   i18n.T("web.files_details"),
		FilesPrev:      i18n.T("web.files_prev"),
		FilesNext:      i18n.T("web.files_next"),
		FilesPath:      i18n.T("web.files_path"),
		NetError:       i18n.T("web.net_error"),

		FilesSearch:      i18n.T("web.files_search"),
		FilesSortName:    i18n.T("web.files_sort_name"),
		FilesSortSize:    i18n.T("web.files_sort_size"),
		FilesSortMod:     i18n.T("web.files_sort_mod"),
		FilesSortDesc:    i18n.T("web.files_sort_desc"),
		FilesPlay:        i18n.T("web.files_play"),
		FilesClose:       i18n.T("web.files_close"),
		FilesSelectAll:   i18n.T("web.files_select_all"),
		FilesBatchDelete: i18n.T("web.files_batch_delete"),
		FilesBatchMoveTo: i18n.T("web.files_batch_move_to"),
		FilesBatchGo:     i18n.T("web.files_batch_go"),
		FilesDrop:        i18n.T("web.files_drop"),
		FilesDirUpload:   i18n.T("web.files_dir_upload"),

		FilesOffline:        i18n.T("web.files_offline"),
		FilesOfflineSave:    i18n.T("web.files_offline_save"),
		FilesOfflineSaved:   i18n.T("web.files_offline_saved"),
		FilesOfflineRemove:  i18n.T("web.files_offline_remove"),
		FilesOfflineRemoved: i18n.T("web.files_offline_removed"),
		FilesOfflineFail:    i18n.T("web.files_offline_fail"),
		FilesOfflineLarge:   i18n.T("web.files_offline_too_large"),
		OfflineHint:         i18n.T("web.offline_hint"),

		Mount:            i18n.T("web.mount"),
		MountOn:          i18n.T("web.mount_on"),
		MountOff:         i18n.T("web.mount_off"),
		MountHint:        i18n.T("web.mount_hint"),
		MountUnavailable: i18n.T("web.mount_unavailable"),
		MountAddr:        i18n.T("web.mount_addr"),
	}
}

// pageData 是整页渲染所需的数据。
type pageData struct {
	Labels pageLabels
	Config string
	// CanSync 决定「立即同步」按钮是否渲染（本地 web 模式没有同步器）
	CanSync bool
	// Browser 非空时渲染文件管理卡片及其首屏内容
	Browser *browserData
	// Mount 非空时渲染挂盘开关卡片
	Mount *mountData
	// Version 是构建版本，显示在页脚，便于用户报问题时对照
	Version string
	// PWA 决定是否输出 manifest/图标并启用离线保存
	PWA pwaData
}

// handlePage 输出内嵌的静态页面。
//
// 输入：canCreate 是否显示建卷卡片；canSync 是否显示同步按钮；configPath 配置文件路径（空串表示不预载）；
// api 文件管理能力；mount 挂盘开关能力；pwa 可安装/离线设置
// 输出：HTML
// 预期：文案、配置正文、文件列表都在服务端渲染，前端只负责主题、进度与离线
func handlePage(canCreate, canSync bool, configPath string, api *filesAPI, mount *MountControl, pwa pwaData) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		data := pageData{Labels: loadLabels(canCreate), CanSync: canSync, Version: version.Version, PWA: pwa}
		if configPath != "" {
			if b, err := os.ReadFile(configPath); err == nil {
				data.Config = string(b)
			}
		}
		if api != nil {
			b := api.browser(r, "", "", "", "")
			data.Browser = &b
		}
		if mount != nil && mount.Status != nil {
			data.Mount = &mountData{Labels: data.Labels, MountState: mount.Status()}
		}
		if err := pageTmpl.Execute(w, data); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}

// handleAsset 输出内嵌的静态资源。
//
// 输入：contentType MIME 类型；data 资源内容
// 输出：HTTP handler
// 预期：内容随二进制固定不变，可长期缓存
func handleAsset(contentType string, data []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		_, _ = w.Write(data)
	}
}

// loadStatus 取当前状态：优先调用方注入，否则只有空状态。
//
// 输入：ctx；status 可选的注入实现
// 输出：任意可 JSON 序列化的状态对象；没有注入时返回空状态
// 预期：不读任何外部进程状态，取不到注入值也返回空列表而不是报错
func loadStatus(ctx context.Context, status StatusFunc) any {
	if status != nil {
		v, err := status(ctx)
		if err != nil {
			return map[string]any{"error": err.Error()}
		}
		return v
	}
	return emptyStatus()
}

// emptyStatus 是没有任何状态来源时的空负载。
func emptyStatus() map[string]any {
	return map[string]any{
		"state":   i18n.T("web.no_instance"),
		"volumes": []any{},
	}
}

// handleStatus 输出 JSON 状态，供程序化调用（面板本身走 /frag/status）。
func handleStatus(status StatusFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		v := loadStatus(r.Context(), status)
		if m, ok := v.(map[string]any); ok {
			if e, has := m["error"]; has {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprint(e)})
				return
			}
		}
		writeJSON(w, http.StatusOK, v)
	}
}

// handleSync 立即增量同步；未注入同步能力时返回 501。
func handleSync(sync SyncFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if sync == nil {
			writeJSON(w, http.StatusNotImplemented, map[string]string{"error": i18n.T("web.sync_unavailable")})
			return
		}
		v, err := sync(r.Context())
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, v)
	}
}

// handleVolumes 创建新卷（POST /api/volumes）。
//
// 输入：JSON {name,title,point}
// 输出：200 结果；400 参数错误；409 重名；501 当前模式不支持；502 Telegram 失败
// 预期：建频道/拉 bot/静音全部在调用方实现，这里只做参数校验与状态码映射
func handleVolumes(create CreateVolumeFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if create == nil {
			writeJSON(w, http.StatusNotImplemented, map[string]string{"error": i18n.T("web.create_unavailable")})
			return
		}
		var req struct {
			Name  string `json:"name"`
			Title string `json:"title"`
			Point string `json:"point"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if strings.TrimSpace(req.Name) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": i18n.T("web.name_required")})
			return
		}
		v, err := create(r.Context(), req.Name, req.Title, req.Point)
		if err != nil {
			code := http.StatusBadGateway
			if errors.Is(err, config.ErrVolumeExists) {
				code = http.StatusConflict
			}
			writeJSON(w, code, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, v)
	}
}

// handleConfig 读写配置文件。
func handleConfig(path string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			b, err := os.ReadFile(path)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = w.Write(b)
		case http.MethodPut:
			b, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			if err := atomic.WriteFile(path, bytes.NewReader(b)); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]string{"ok": "saved"})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

// ---- htmx 片段接口 ----
//
// 面板不再手写 fetch 与 DOM 拼接：轮询和提交都打这些接口，服务端直接返回 HTML。
// 状态类片段用 hx-swap-oob 就地替换状态条、统计与卷列表，整页不刷新。

// renderFragment 输出 HTML 片段。
//
// 输入：w 响应；tmpl 模板；data 模板数据
// 输出：无
// 预期：一律 no-store，避免浏览器缓存旧状态
func renderFragment(w http.ResponseWriter, tmpl *template.Template, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := tmpl.Execute(w, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// handleFragStatus 返回状态片段：状态条、统计、卷列表、下载进度。
func handleFragStatus(status StatusFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		l := loadLabels(false)
		renderFragment(w, statusFrag, statusFragData{
			Labels:     l,
			statusView: viewOf(loadStatus(r.Context(), status), l),
		})
	}
}

// handleFragSync 立即同步一次并返回反馈。
//
// 输入：sync 同步实现，nil 表示当前模式不支持
// 输出：反馈 HTML
// 预期：出错也返回 200 + 错误文案，htmx 默认不替换 4xx/5xx 的响应体
func handleFragSync(sync SyncFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		l := loadLabels(false)
		msg := msgData{Class: "ok", Text: l.SyncDone}
		if sync == nil {
			msg = msgData{Class: "err", Text: i18n.T("web.sync_unavailable")}
		} else if _, err := sync(r.Context()); err != nil {
			msg = msgData{Class: "err", Text: err.Error()}
		}
		renderFragment(w, msgFrag, msg)
	}
}

// handleFragConfig 保存配置正文（表单字段 content）。
func handleFragConfig(path string, bus *eventbus.Bus) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		l := loadLabels(false)
		if err := r.ParseForm(); err != nil {
			renderFragment(w, msgFrag, msgData{Class: "err", Text: err.Error()})
			return
		}
		if err := atomic.WriteFile(path, strings.NewReader(r.PostFormValue("content"))); err != nil {
			renderFragment(w, msgFrag, msgData{Class: "err", Text: err.Error()})
			return
		}
		// 通知其它已打开的页面：配置变了，重新拉正文
		if bus != nil {
			bus.Publish("config")
		}
		renderFragment(w, msgFrag, msgData{Class: "ok", Text: l.Saved})
	}
}

// handleFragVolume 建卷（表单字段 name/title/point）。
//
// 输出：反馈 HTML；成功时附带改写后的配置正文
// 预期：重名/参数错误都返回 200 + 错误文案，由页面就地显示
func handleFragVolume(create CreateVolumeFunc, configPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		l := loadLabels(true)
		if create == nil {
			renderFragment(w, msgFrag, msgData{Class: "err", Text: i18n.T("web.create_unavailable")})
			return
		}
		if err := r.ParseForm(); err != nil {
			renderFragment(w, msgFrag, msgData{Class: "err", Text: err.Error()})
			return
		}
		name := strings.TrimSpace(r.PostFormValue("name"))
		if name == "" {
			renderFragment(w, msgFrag, msgData{Class: "err", Text: l.NameNeed})
			return
		}
		title := strings.TrimSpace(r.PostFormValue("title"))
		point := strings.TrimSpace(r.PostFormValue("point"))
		if _, err := create(r.Context(), name, title, point); err != nil {
			renderFragment(w, msgFrag, msgData{Class: "err", Text: err.Error()})
			return
		}
		msg := msgData{Class: "ok", Text: l.Created + " · " + l.Restart}
		if configPath != "" {
			if b, err := os.ReadFile(configPath); err == nil {
				msg.Config = string(b)
			}
		}
		renderFragment(w, volumeFrag, msg)
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
