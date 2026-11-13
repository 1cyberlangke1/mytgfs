package web

// 面板服务：桌面壳与 `mytgfs web` 共用同一套 JSON API 与内嵌前端。
//
// 桌面壳把 NewHandler 的结果挂到 webview 上，不监听端口；
// `mytgfs web` 用 ServeWith 监听回环地址。

import (
	"context"
	"errors"
	"net/http"

	"mytgfs/internal/channel"
	"mytgfs/internal/eventbus"
	"mytgfs/internal/fm"
)

// StatusFunc 返回面板需要的状态快照。
type StatusFunc func(ctx context.Context) (any, error)

// SyncFunc 立即触发一次增量同步并返回结果。
type SyncFunc func(ctx context.Context) (any, error)

// CreateVolumeFunc 创建新卷并返回可序列化的结果。
//
// 输入：ctx；name 卷名；title 频道标题；point 挂载点
// 输出：结果对象；error
// 预期：重名返回 config.ErrVolumeExists，供 handler 映射成 409
type CreateVolumeFunc func(ctx context.Context, name, title, point string) (any, error)

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
	// Files 返回指定卷的文件管理后端；nil 表示不提供文件管理
	Files func(volume string) (fm.Files, bool)
	// Volumes 返回可管理卷名，按配置顺序
	Volumes func() []string
	// Stats 返回卷内文件数与已用字节；nil 时用量显示 0
	Stats func(volume string) (files, used int64, ok bool)
	// TempDir 是分片上传的临时目录；空则 data/tmp
	TempDir string
	// Mount 控制 WebDAV 挂盘开关；nil 表示不支持
	Mount *MountControl
	// Bus 用于把配置/挂载变更推给托盘；nil 时只影响当前页面
	Bus *eventbus.Bus
	// ThumbDir 是缩略图缓存目录；空则 data/thumbs
	ThumbDir string
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
// 输出：*http.ServeMux
// 预期：所有 /api/* 路由先于 "/" 注册，前端路由不会吞掉接口
func newMux(configPath string, opts Options) *http.ServeMux {
	mux := http.NewServeMux()
	api := newFilesAPI(opts.Files, opts.Volumes, opts.Stats, opts.ThumbDir)
	if api != nil {
		api.setBus(opts.Bus)
	}

	mux.Handle("/api/state", handleAPIState(api, opts))
	mux.Handle("/api/files", handleAPIFiles(api))
	mux.Handle("/api/files/mkdir", handleAPIMkdir(api))
	mux.Handle("/api/files/move", handleAPIMove(api))
	mux.Handle("/api/files/remove", handleAPIRemove(api))
	mux.Handle("/api/files/upload", handleAPIUpload(api))
	mux.Handle("/api/file", handleAPIFile(api))
	mux.Handle("/api/thumb", handleFileThumb(api))
	mux.Handle("/api/cover", handleVolumeCover(api))
	mux.Handle("/api/volumes", handleAPIVolumes(opts))
	mux.Handle("/api/volumes/attach", handleAPIAttachVolume(opts))
	mux.Handle("/api/channels", handleAPIChannels(opts))
	mux.Handle("/api/settings", handleAPISettings(configPath))
	mux.Handle("/api/mount", handleAPIMount(opts))
	mux.Handle("/api/sync", handleAPISync(opts))
	mux.Handle("/api/events", handleAPIEvents(opts.Bus))

	// 分片上传会话（大文件续传）
	up := newUploadAPI(opts.Files, opts.TempDir)
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
			replyJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	})

	mux.Handle("/", spaHandler())
	return mux
}
