// Package app 是唯一的装配根：读配置、解析卷、开索引、连 Telegram、造 store。
//
// 输入：Options（配置/会话/密钥路径，零值即默认）、卷名
// 输出：Run 回调内可用的 Session（索引 + 连接 + store 都已就绪）
// 预期：
//   - 索引与连接池的关闭由 Run 统一收口，调用方不再自己拼对象图
//   - 同一进程内同一个卷只打开一次索引、只建一次连接池
package app

import (
	"cmp"
	"context"
	"fmt"
	"os"

	"mytgfs/internal/channel"
	"mytgfs/internal/config"
	"mytgfs/internal/crypto"
	"mytgfs/internal/datadir"
	"mytgfs/internal/eventbus"
	"mytgfs/internal/i18n"
	"mytgfs/internal/index"
	"mytgfs/internal/sessionstore"
	"mytgfs/internal/telegramstore"
	"mytgfs/internal/tgclient"
	"mytgfs/internal/uploadcache"
)

// 默认路径与命令行既有约定一致；Options 零值直接可用。
const (
	DefaultConfigPath  = "config.toml"
	DefaultSessionPath = "secrets/bot.session"
	// DefaultUserSessionPath 是 user 会话（建频道、列频道这类 bot 做不到的操作）。
	DefaultUserSessionPath = "secrets/user.session"
	DefaultKeyPath         = "secrets/session.key"
	// DefaultVolumeName 是无 [[volumes]] 段时的隐式卷名。
	DefaultVolumeName = "mytgfs"
)

// Options 是装配参数。
type Options struct {
	ConfigPath  string // 默认 config.toml
	SessionPath string // 默认 secrets/bot.session
	// UserSessionPath 是 user 会话路径，默认 secrets/user.session
	UserSessionPath string
	KeyPath         string // 默认 secrets/session.key
	// Bus 可选：非空时卷的可见变化会以 status 事件推给订阅者
	Bus *eventbus.Bus
}

// ErrVolumeNotFound 表示配置里没有这个卷。
type ErrVolumeNotFound struct{ Name string }

func (e ErrVolumeNotFound) Error() string { return "volume not found: " + e.Name }

// ErrMissingAPIHash 表示配置里没写 api_hash。
// 用类型而不是包级变量：文案要按当前语言惰性求值。
type errMissingAPIHash struct{}

func (errMissingAPIHash) Error() string { return i18n.T("err.missing_api_hash") }

var ErrMissingAPIHash error = errMissingAPIHash{}

// ErrUserSessionMissing 表示本机还没有 user 会话。
//
// 列表/建频道这类操作 bot 会话做不到，必须先 `mytgfs auth login --user` 登录一次。
type errUserSessionMissing struct{}

func (errUserSessionMissing) Error() string { return i18n.T("err.user_session_missing") }

var ErrUserSessionMissing error = errUserSessionMissing{}

// App 持有已读取的配置，后续所有装配都基于它。
type App struct {
	cfg  config.Config
	opts Options
}

// Load 读取配置。
//
// 输入：opts 路径参数，空字段回落默认值
// 输出：已加载的 App；配置文件读取失败时返回错误
func Load(opts Options) (*App, error) {
	opts.ConfigPath = cmp.Or(opts.ConfigPath, DefaultConfigPath)
	cfg, err := config.Load(opts.ConfigPath)
	if err != nil {
		return nil, err
	}
	return &App{cfg: cfg, opts: opts}, nil
}

// Config 返回底层配置，供调用方读取并发数、端口等只读字段。
func (a *App) Config() config.Config { return a.cfg }

// Volume 解析卷名与卷定义。
//
// 输入：name 卷名；空字符串表示取第一个卷
// 输出：卷定义、解析后的卷名；ok=false 表示配置里没有这个卷
// 预期：无 [[volumes]] 段时退化为单卷 mytgfs（chat_id 取 [telegram].channel_id）
func (a *App) Volume(name string) (config.Volume, string, bool) {
	resolved := name
	if resolved == "" {
		if len(a.cfg.Volumes) > 0 {
			resolved = a.cfg.Volumes[0].Name
		} else {
			resolved = DefaultVolumeName
		}
	}
	if len(a.cfg.Volumes) == 0 {
		if resolved != DefaultVolumeName {
			return config.Volume{}, resolved, false
		}
		return config.Volume{Name: resolved, ChatID: a.cfg.Telegram.ChannelID}, resolved, true
	}
	for _, v := range a.cfg.Volumes {
		if v.Name == resolved {
			return v, resolved, true
		}
	}
	return config.Volume{}, resolved, false
}

// Index 打开卷的本地索引；调用方负责 Close。
func (a *App) Index(volumeName string) (*index.Index, error) {
	return index.Open(datadir.Path("index-" + volumeName + ".db"))
}

// Channels 用 user 会话列出当前账号可管理的频道。
//
// 输入：ctx（整体超时由调用方决定）
// 输出：频道列表，含原始 id 与 -100... 形式的 chat_id
// 预期：
//   - 临时开一条 user 连接，取完列表立即退出，不常驻
//   - 只返回自己创建或有管理权限的频道/超级群；bot 会话无法列出对话
//   - user 会话文件不存在时返回 ErrUserSessionMissing
func (a *App) Channels(ctx context.Context) ([]channel.Item, error) {
	userSession := a.userSessionPath()
	if _, err := os.Stat(userSession); err != nil {
		return nil, ErrUserSessionMissing
	}
	apiHash := a.cfg.APIHash()
	if apiHash == "" {
		return nil, ErrMissingAPIHash
	}
	key, err := crypto.LoadOrCreateKey(cmp.Or(a.opts.KeyPath, DefaultKeyPath))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", i18n.T("err.session_key_prefix"), err)
	}
	client := tgclient.New(a.cfg.Telegram.APIID, apiHash,
		&sessionstore.Encrypted{Path: userSession, Key: key},
		tgclient.Options{})

	var items []channel.Item
	var listErr error
	runErr := client.Run(ctx, func(ctx context.Context) error {
		items, listErr = channel.List(ctx, client.API())
		// 返回 nil 让 gotd 收尾退出；真正的列表错误单独带出去，避免被包成 callback 错误
		return nil
	})
	if listErr != nil {
		return nil, listErr
	}
	if runErr != nil {
		return nil, runErr
	}
	return items, nil
}

// Session 是 Run 回调内可用的已连接卷。
type Session struct {
	Volume config.Volume // 卷定义
	Name   string        // 解析后的卷名
	Index  *index.Index  // 本地索引
	Client *tgclient.Client
	API    telegramstore.API

	app   *App
	own   *telegramstore.Store
	extra map[int64]*telegramstore.Store
}

// Store 返回本卷的 store（已配好并发、断点续传）。
func (s *Session) Store() *telegramstore.Store {
	if s.own == nil {
		s.own = s.app.newStore(s.API, s.Volume.ChatID)
	}
	return s.own
}

// StoreFor 返回任意频道的 store；与卷同频道时复用同一个实例。
//
// 输入：chatID 目标频道
// 输出：可直接读写的 store
func (s *Session) StoreFor(chatID int64) *telegramstore.Store {
	if chatID == s.Volume.ChatID {
		return s.Store()
	}
	if st, ok := s.extra[chatID]; ok {
		return st
	}
	st := s.app.newStore(s.API, chatID)
	if s.extra == nil {
		s.extra = make(map[int64]*telegramstore.Store)
	}
	s.extra[chatID] = st
	return st
}

// Run 装配一个卷的完整依赖并在回调内执行。
//
// 输入：ctx；name 卷名（空取第一个）；fn 业务回调
// 输出：fn 的错误原样返回；配置/密钥/索引/连接失败时返回相应错误
// 预期：返回前已关闭索引与连接池；fn 内不得持有 Index 到回调之外
func (a *App) Run(ctx context.Context, name string, fn func(context.Context, *Session) error) error {
	vol, volName, ok := a.Volume(name)
	if !ok {
		return ErrVolumeNotFound{Name: volName}
	}
	apiHash := a.cfg.APIHash()
	if apiHash == "" {
		return ErrMissingAPIHash
	}
	key, err := crypto.LoadOrCreateKey(cmp.Or(a.opts.KeyPath, DefaultKeyPath))
	if err != nil {
		return fmt.Errorf("%s: %w", i18n.T("err.session_key_prefix"), err)
	}
	idx, err := a.Index(volName)
	if err != nil {
		return err
	}
	defer idx.Close()

	client := tgclient.New(a.cfg.Telegram.APIID, apiHash,
		&sessionstore.Encrypted{Path: cmp.Or(a.opts.SessionPath, DefaultSessionPath), Key: key},
		tgclient.Options{})

	return client.Run(ctx, func(ctx context.Context) error {
		api, closePool, perr := client.PooledAPI(a.cfg.Mount.DownloadConnections())
		if perr != nil {
			return perr
		}
		defer closePool()
		return fn(ctx, &Session{
			Volume: vol, Name: volName, Index: idx,
			Client: client, API: api, app: a,
		})
	})
}

// newStore 按配置造一个已就绪的 store。
func (a *App) newStore(api telegramstore.API, chatID int64) *telegramstore.Store {
	st := telegramstore.New(api, chatID)
	st.SetParallel(a.cfg.Mount.DownloadConnections())
	st.SetUploadParallel(a.cfg.Mount.UploadParallelism())
	st.SetResumeStore(uploadcache.New(datadir.Path("uploads")))
	return st
}
