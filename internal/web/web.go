package web

// 面板服务：桌面壳与 `mytgfs web` 共用同一套 JSON API 与内嵌前端。
//
// 桌面壳把 NewHandler 的结果挂到 webview 上，不监听端口；
// `mytgfs web` 用 ServeWith 监听回环地址。

import (
	"context"
	"errors"
	"net/http"

	"mytgfs/internal/authsvc"
	"mytgfs/internal/channel"
	"mytgfs/internal/fm"
	"mytgfs/internal/push"
)

// StatusFunc 返回面板需要的状态快照。
type StatusFunc func(ctx context.Context) (any, error)

// SyncFunc 立即触发一次增量同步并返回结果。
type SyncFunc func(ctx context.Context) (any, error)

// CreateVolumeFunc 创建新卷并返回可序列化的结果。
//
// 输入：ctx；name 卷名；title 频道标题
// 输出：结果对象；error
// 预期：重名返回 config.ErrVolumeExists，供 handler 映射成 409
type CreateVolumeFunc func(ctx context.Context, name, title string) (any, error)

// DeleteVolumeFunc 删除一个卷（频道 + 配置 + 本地索引）。
type DeleteVolumeFunc func(ctx context.Context, name string) error

// AttachVolumeFunc 把已有频道挂成卷。
//
// 输入：ctx；name 卷名；chatID Bot API 形式或裸频道 id
// 输出：卷信息；error
// 预期：频道不可见或 bot 无发言权时报错，不写配置
type AttachVolumeFunc func(ctx context.Context, name string, chatID int64) (any, error)

// ChannelsFunc 列出 user 会话可管理的频道。
//
// 输入：ctx
// 输出：频道摘要（含 -100... 形式的 chat_id）
// 预期：只读；user 会话缺失时报错，由 handler 转成人话
type ChannelsFunc func(ctx context.Context) ([]channel.Item, error)

// Options 是面板的可选能力，零值表示只提供只读状态。
type Options struct {
	Status StatusFunc
	Sync   SyncFunc
	// CreateVolume 为 nil 时建卷接口返回 501
	CreateVolume CreateVolumeFunc
	// DeleteVolume 为 nil 时删卷接口返回 501
	DeleteVolume DeleteVolumeFunc
	// AttachVolume 为 nil 时「添加已有频道」返回 501
	AttachVolume AttachVolumeFunc
	// Channels 为 nil 时频道下拉为空，只能手填 chat_id
	Channels ChannelsFunc
	// Auth 提供凭证填写与登录；nil 时面板不显示账号卡
	Auth *authsvc.Service
	// Files 返回指定卷的文件管理后端；nil 表示不提供文件管理
	Files func(volume string) (fm.Files, bool)
	// Volumes 返回可管理卷名，按配置顺序
	Volumes func() []string
	// Stats 返回卷内文件数与已用字节；nil 时用量显示 0
	Stats func(volume string) (files, used int64, ok bool)
	// TempDir 是分片上传的临时目录；空则 data/tmp
	TempDir string
	// DownloadsDir 是下载目录；空则 data/downloads
	DownloadsDir string
	// Push 是推送分发点：写操作成功后自动通知面板；nil 时不实时刷新
	Push *push.Hub
	// ThumbDir 是缩略图缓存目录；空则 data/thumbs
	ThumbDir string
	// Autostart 控制开机自启；nil 时接口返回 501、面板不显示开关
	Autostart AutostartControl
	// ClearQueue 删除所有已结束（成功/失败）的任务记录；nil 时接口返回 501
	ClearQueue func(ctx context.Context) (int64, error)
	// Cover 读写频道头像；nil 时封面接口返回 501、面板不显示封面设置
	Cover CoverSource
}

// NewHandler 返回面板的完整路由，供桌面壳直接挂在 webview 上。
//
// 输入：configPath 配置文件路径；opts 面板能力
// 输出：http.Handler
// 预期：与 ServeWith 使用同一套路由，不监听任何端口
func NewHandler(configPath string, opts Options) http.Handler {
	return newMux(configPath, opts)
}

// ServeWith 在 addr 上提供面板，ctx 取消时优雅关闭。
//
// 输入：ctx；addr 监听地址；configPath 配置文件；opts 面板能力
// 输出：error
// 预期：ctx 取消时优雅关闭；未就绪的 handler 返回空状态而不是报错
func ServeWith(ctx context.Context, addr, configPath string, opts Options) error {
	srv := &http.Server{Addr: addr, Handler: newMux(configPath, opts)}
	context.AfterFunc(ctx, func() {
		_ = srv.Shutdown(context.Background())
	})
	err := srv.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// newMux 组装 JSON API 与内嵌前端。
//
// 输入：configPath 配置文件路径；opts 面板能力
// 输出：*router（写操作在注册点统一包上「成功即推送」）
// 预期：所有 /api/* 路由先于 "/" 注册，前端路由不会吞掉接口
func newMux(configPath string, opts Options) *router {
	rt := newRouter(opts.Push)
	api := newFilesAPI(opts.Files, opts.Volumes, opts.Stats, opts.ThumbDir)

	rt.route("/api/state", handleAPIState(api, opts))
	rt.route("/api/files", handleAPIFiles(api))
	rt.route("/api/files/mkdir", handleAPIMkdir(api))
	rt.route("/api/files/move", handleAPIMove(api))
	rt.route("/api/files/remove", handleAPIRemove(api))
	rt.route("/api/file", handleAPIFile(api))
	dl := newDownloadStore(resolveDownloadsDir(configPath, opts.DownloadsDir), func(v string) (fm.Files, bool) {
		if api == nil {
			return nil, false
		}
		return api.lookup(v)
	}, opts.Push)
	rt.route("/api/downloads", handleAPIDownloads(dl))
	rt.route("/api/downloads/task", handleAPIDownloadTask(dl))
	rt.route("/api/downloads/open", handleAPIDownloadsOpen(dl))
	rt.route("/api/downloads/file", handleAPIDownloadFile(dl))
	rt.route("/api/downloads/open-file", handleAPIDownloadOpenFile(dl))
	rt.route("/api/thumb", handleFileThumb(api))
	rt.route("/api/cover", handleVolumeCover(opts.Cover))
	rt.route("/api/volumes", handleAPIVolumes(opts))
	rt.route("/api/volumes/attach", handleAPIAttachVolume(opts))
	rt.route("/api/channels", handleAPIChannels(opts))
	rt.route("/api/credentials", handleAPICredentials(opts))
	rt.route("/api/login/bot", handleAPILoginBot(opts))
	rt.route("/api/login/user/start", handleAPIUserStart(opts))
	rt.route("/api/login/user/code", handleAPIUserFinish(opts))
	rt.route("/api/identity", handleAPIIdentity(opts))
	rt.route("/api/settings", handleAPISettings(configPath))
	rt.route("/api/autostart", handleAPIAutostart(opts))
	rt.route("/api/sync", handleAPISync(opts))
	rt.route("/api/queue", handleAPIQueue(opts))
	rt.route("/api/events", handleAPIEvents(opts.Push))
	// 网页版没有 Wails 框架默认链：给空运行时脚本，避免 index.html 里 script 报错；
	// 桌面壳该路径由框架在进入 mux 之前处理，不会到这里。
	rt.routeFunc("/wails/runtime.js", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		w.WriteHeader(http.StatusOK)
	})

	// 分片上传会话（大文件续传）
	up := newUploadAPI(opts.Files, opts.TempDir)
	rt.routeFunc("/api/upload/init", up.handleInit)
	rt.routeFunc("/api/upload/chunk", up.handleChunk)
	rt.routeFunc("/api/upload/commit", up.handleCommit)
	rt.routeFunc("/api/upload", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			up.handleStatus(w, r)
		case http.MethodDelete:
			up.handleAbort(w, r)
		default:
			replyJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	})

	rt.route("/", spaHandler())
	return rt
}
