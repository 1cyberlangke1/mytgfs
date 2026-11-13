package trayapp

// 托盘主管进程：挂载卷、自动增量同步、按需开关 Web 面板、托盘菜单。
//
// 这个包不依赖命令行参数解析，方便 CLI 的 `tray` 子命令和
// GUI 子系统的 `mytgfs-tray.exe` 共用同一套逻辑。

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"mytgfs/internal/app"
	"mytgfs/internal/authsvc"
	"mytgfs/internal/autostart"
	"mytgfs/internal/brand"
	"mytgfs/internal/channel"
	"mytgfs/internal/config"
	"mytgfs/internal/crypto"
	"mytgfs/internal/datadir"
	"mytgfs/internal/dav"
	"mytgfs/internal/davsvc"
	"mytgfs/internal/eventbus"
	"mytgfs/internal/fm"
	"mytgfs/internal/i18n"
	"mytgfs/internal/index"
	"mytgfs/internal/queue"
	"mytgfs/internal/sessionstore"
	tgsync "mytgfs/internal/sync"
	"mytgfs/internal/telegramstore"
	"mytgfs/internal/tgclient"
	"mytgfs/internal/volume"
	"mytgfs/internal/web"
)

// Options 是托盘启动参数。
type Options struct {
	ConfigPath string // 配置文件路径，空则 config.toml
	Point      string // 挂载点覆盖，空则取配置
}

// SyncInterval 把配置里的秒数换算成自动同步间隔。
//
// 输入：seconds 配置值
// 输出：同步间隔；0 表示关闭
// 预期：0 用默认 15 秒，负数关闭，正数按秒
func SyncInterval(seconds int) time.Duration {
	switch {
	case seconds < 0:
		return 0
	case seconds == 0:
		return 15 * time.Second
	default:
		return time.Duration(seconds) * time.Second
	}
}

// AutoSync 周期性把频道差异应用进索引并通知挂载点刷新。
//
// 输入：ctx；notify 路径变更回调（可为 nil，纯面板模式没有挂载点要通知）；
//
//	idx 索引；d 差异源；interval 间隔；
//	mu 与手动同步共用的互斥锁（可为 nil）
//
// 输出：无（ctx 取消时返回）
// 预期：启动即同步一次；单轮失败只记日志不退出；有变化才回调
func AutoSync(ctx context.Context, notify func(changed, removed []string), idx *index.Index, d tgsync.ChannelDiffer, st tgsync.NodeSource, interval time.Duration, mu *sync.Mutex) {
	AutoSyncWhile(ctx, notify, idx, d, st, interval, mu, nil)
}

// StatusNotifier 把自动同步的增删转成 status 事件，面板据此刷新。
//
// 输入：bus 事件总线；nil 时返回 nil
// 输出：AutoSync 的 notify 回调
// 预期：只有真的增删了条目才推送，空轮不打扰面板
func StatusNotifier(bus *eventbus.Bus) func(changed, removed []string) {
	if bus == nil {
		return nil
	}
	return func(changed, removed []string) {
		if len(changed) == 0 && len(removed) == 0 {
			return
		}
		bus.Publish("status")
	}
}

// SyncSwitch 是自动同步的运行开关。
//
// 输入：由托盘菜单切换；nil 等价于常开
// 输出：On() 当前状态
// 预期：关掉后只跳过同步，不动卷与索引
type SyncSwitch struct {
	on atomic.Bool
}

// NewSyncSwitch 创建开关并设定初始状态。
func NewSyncSwitch(on bool) *SyncSwitch {
	s := &SyncSwitch{}
	s.on.Store(on)
	return s
}

// On 返回当前是否启用。
func (s *SyncSwitch) On() bool {
	return s == nil || s.on.Load()
}

// Set 设置开关状态。
func (s *SyncSwitch) Set(on bool) {
	if s != nil {
		s.on.Store(on)
	}
}

// AutoSyncWhile 与 AutoSync 相同，但每轮先问 enabled：
// 返回 false 时跳过本轮（只等下一个 tick），用于托盘菜单里随时开关。
func AutoSyncWhile(ctx context.Context, notify func(changed, removed []string), idx *index.Index, d tgsync.ChannelDiffer, st tgsync.NodeSource, interval time.Duration, mu *sync.Mutex, enabled func() bool) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	syncOnce := func() (tgsync.ChannelSyncResult, error) {
		if mu != nil {
			mu.Lock()
			defer mu.Unlock()
		}
		return tgsync.SyncChannel(ctx, idx, d, st, 100)
	}
	for {
		if enabled == nil || enabled() {
			res, err := syncOnce()
			if ctx.Err() != nil {
				return
			}
			switch {
			case err != nil:
				fmt.Fprintf(os.Stderr, "%s: %v\n", i18n.T("log.autosync_failed"), err)
			case (res.Pulled > 0 || res.Deleted > 0) && notify != nil:
				notify(res.Changed, res.Removed)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Run 运行托盘主管进程，阻塞到「卸载并退出」。
//
// 输入：opts 启动参数
// 输出：只有启动阶段失败才返回 error；正常退出返回 nil
// 预期：网络不可用时托盘与挂载保持存在，客户端自动重连；
//
//	Web 面板只在菜单「打开面板」时启动，「关闭面板」立即停止
func Run(opts Options) error {
	cfgPath := cmp.Or(opts.ConfigPath, "config.toml")

	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("%s: %w", i18n.T("err.config_read"), err)
	}
	// 挂载点优先级：命令行 -point > config.toml [mount].point
	point := opts.Point
	if point == "" {
		point = cfg.Mount.Point
	}
	apiHash := cfg.APIHash()
	if apiHash == "" {
		return fmt.Errorf("%s", i18n.T("err.missing_api_hash"))
	}
	key, err := crypto.LoadOrCreateKey("secrets/session.key")
	if err != nil {
		return fmt.Errorf("%s: %w", i18n.T("err.session_key_prefix"), err)
	}

	rootCtx, rootCancel := context.WithCancel(context.Background())
	defer rootCancel()

	var mu sync.Mutex
	var cancels []context.CancelFunc
	var wg sync.WaitGroup

	// ---- 桌面壳（Wails）：窗口 + 托盘，不监听端口、不打开浏览器 ----
	var hmu sync.RWMutex
	inner := http.Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<!doctype html><meta charset=utf-8><body style=\"font:14px system-ui;padding:24px\">mytgfs</body>"))
	}))
	setHandler := func(h http.Handler) { hmu.Lock(); inner = h; hmu.Unlock() }

	wailsApp := application.New(application.Options{
		Name:        "mytgfs",
		Description: "Telegram channel file volumes",
		Icon:        brand.Large,
		Assets: application.AssetOptions{
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hmu.RLock()
				h := inner
				hmu.RUnlock()
				h.ServeHTTP(w, r)
			}),
		},
	})
	win := wailsApp.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:   "main",
		Title:  "mytgfs",
		Width:  1120,
		Height: 760,
		URL:    "/",
	})
	// 关窗只隐藏，托盘与后台同步继续运行
	win.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		e.Cancel()
		win.Hide()
	})
	// 调试用：设置 MYTGFS_DEBUG_SHOW=1 时启动直接显示主窗口
	if os.Getenv("MYTGFS_DEBUG_SHOW") != "" {
		go func() {
			time.Sleep(3 * time.Second)
			win.Show()
			win.Focus()
		}()
	}

	sysTray := wailsApp.SystemTray.New()
	if runtime.GOOS == "windows" {
		sysTray.SetIcon(brand.ICO)
	} else {
		sysTray.SetIcon(brand.PNG256)
	}

	// ready 之前不能碰 Wails 的 UI 对象：InvokeSync 依赖主循环
	var ready atomic.Bool
	uiUpdate := func(fn func()) {
		if !ready.Load() {
			return
		}
		application.InvokeSync(fn)
	}

	var statusItem, davItem, syncItem, autoItem *application.MenuItem

	// trayVolume 是一个已挂载卷的运行时句柄，供状态展示与手动同步使用。
	type trayVolume struct {
		name   string
		idx    *index.Index
		davfs  *dav.FileSystem
		fmgr   *fm.Manager
		store  *telegramstore.Store
		differ tgsync.ChannelDiffer
		mu     sync.Mutex // 自动同步与手动同步互斥
	}
	var volMu sync.Mutex
	var volumes []*trayVolume

	// 自动同步总开关：默认跟随配置，托盘菜单可即时开/关并写回 config.toml
	syncSwitch := NewSyncSwitch(cfg.Mount.SyncSeconds >= 0)

	var stateMu sync.Mutex
	// connKey 是状态对应的语言包键，state_code 由它映射；connText 只用于展示
	connKey := "tray.state.starting"
	connText := i18n.T(connKey)
	setState := func(key string) {
		text := i18n.T(key)
		stateMu.Lock()
		connKey, connText = key, text
		stateMu.Unlock()
		uiUpdate(func() {
			if statusItem != nil {
				statusItem.SetLabel(text)
			}
			sysTray.SetTooltip("mytgfs · " + text)
		})
	}

	// 授权失效标记：Web 面板据此显示重新登录提示，并禁用写操作
	var authLost atomic.Bool
	// botUsername 在连接就绪后读取，建卷时用来拉 bot 进新频道
	var botMu sync.Mutex
	var botUsername string
	setBotUsername := func(n string) {
		botMu.Lock()
		botUsername = n
		botMu.Unlock()
	}
	getBotUsername := func() string {
		botMu.Lock()
		defer botMu.Unlock()
		return botUsername
	}

	unmountAll := func() {
		mu.Lock()
		cs := cancels
		cancels = nil
		mu.Unlock()
		for _, c := range cs {
			c()
		}
	}

	// ---- 可选 WebDAV 挂盘服务 ----
	//
	// 默认关闭；Web 面板与托盘菜单都能启停，状态写回 [webdav].enabled。
	// 只暴露一个卷（[webdav].volume，空则第一个启用的卷）。
	davSvc := &davsvc.Service{}
	// bus 把配置/挂载变更立刻推给面板与托盘，不走轮询
	bus := eventbus.New()

	// 托盘勾选状态订阅事件：无论从面板还是菜单触发，都会同步
	mountCh, cancelMountSub := bus.Subscribe("mount")
	defer cancelMountSub()
	go func() {
		for {
			select {
			case <-rootCtx.Done():
				return
			case _, ok := <-mountCh:
				if !ok {
					return
				}
				uiUpdate(func() {
					if davItem != nil {
						davItem.SetChecked(davSvc.State().Enabled)
					}
				})
			}
		}
	}()

	davState := func() web.MountState {
		st := davSvc.State()
		return web.MountState{
			Available: st.Enabled || len(volumes) > 0,
			Enabled:   st.Enabled,
			Addr:      st.Addr,
			Volume:    st.Volume,
		}
	}

	// findVolume 取已挂载卷的运行时句柄。
	findVolume := func(name string) *trayVolume {
		volMu.Lock()
		defer volMu.Unlock()
		for _, v := range volumes {
			if v.name == name {
				return v
			}
		}
		return nil
	}

	stopDAV := func() error {
		was := davSvc.State().Enabled
		if err := davSvc.Stop(); err != nil {
			return fmt.Errorf("%s: %w", i18n.T("err.webdav_stop"), err)
		}
		if was {
			bus.Publish("mount")
		}
		return nil
	}

	startDAV := func() error {
		if davSvc.State().Enabled {
			return nil
		}
		// 重新读配置：面板/手动编辑过的 [webdav] 段以文件为准
		cur, cerr := config.Load(cfgPath)
		if cerr != nil {
			return fmt.Errorf("%s: %w", i18n.T("err.config_load"), cerr)
		}
		vol, ok := cur.WebDAV.Target(cur.Volumes)
		if !ok {
			return fmt.Errorf("%s", i18n.T("err.webdav_no_volume"))
		}
		tv := findVolume(vol.Name)
		if tv == nil || tv.davfs == nil {
			return fmt.Errorf("%s: %s", i18n.T("err.webdav_no_volume"), vol.Name)
		}
		if _, err := davSvc.Start(cur.WebDAV.ListenAddr(), vol.Name, tv.davfs); err != nil {
			return fmt.Errorf("%s: %w", i18n.T("err.webdav_start"), err)
		}
		bus.Publish("mount")
		return nil
	}

	// setDAV 启停并落盘，托盘与面板共用。
	setDAV := func(on bool) error {
		var derr error
		if on {
			derr = startDAV()
		} else {
			derr = stopDAV()
		}
		if derr != nil {
			return derr
		}
		w := config.WebDAV{Enabled: on, Addr: cfg.WebDAV.ListenAddr(), Volume: cfg.WebDAV.Volume}
		if werr := config.SetWebDAV(cfgPath, w); werr != nil {
			// 服务已经启停成功，配置失败只提醒，不撤销
			fmt.Fprintf(os.Stderr, "%s: %v\n", i18n.T("err.webdav_config"), werr)
		} else {
			bus.Publish("config")
		}
		return nil
	}

	client := tgclient.New(cfg.Telegram.APIID, apiHash,
		&sessionstore.Encrypted{Path: "secrets/bot.session", Key: key},
		tgclient.Options{
			OnConnState: func(s tgclient.ConnState) {
				switch s {
				case tgclient.ConnReady:
					setState("tray.state.connected")
				case tgclient.ConnConnecting:
					setState("tray.state.connecting")
				default:
					setState("tray.state.reconnecting")
				}
			},
			OnAuthLost: func(_ context.Context, err error) error {
				// 会话被吊销只能重新登录；停止 Run，别让连接循环空转
				authLost.Store(true)
				setState("tray.state.auth_lost")
				fmt.Fprintf(os.Stderr, "%s: %v\n", i18n.T("log.session_invalid"), err)
				return err
			},
		})

	// 面板路由：桌面壳直接挂在 webview 上，不监听端口
	buildPanel := func() http.Handler {
		webOpts := web.Options{
			Auth: authsvc.New(authsvc.Paths{ConfigPath: cfgPath}),
			// 桌面壳里的自启开关与托盘菜单写同一处注册项
			Autostart: autostart.Control{},
			Status: func(ctx context.Context) (any, error) {
				stateMu.Lock()
				st := connText
				code := stateCode(connKey)
				stateMu.Unlock()
				volMu.Lock()
				vs := append([]*trayVolume(nil), volumes...)
				volMu.Unlock()
				list := make([]map[string]any, 0, len(vs))
				for _, v := range vs {
					children, err := v.idx.Children(ctx, 0)
					if err != nil {
						list = append(list, map[string]any{"name": v.name, "error": err.Error()})
						continue
					}
					used, _ := v.idx.Used(ctx)
					list = append(list, map[string]any{
						"name": v.name, "entries": len(children), "used": used,
					})
				}
				auth := "ok"
				if authLost.Load() {
					auth = "lost"
				}
				// 下载进度：卷名 + 路径，供面板显示「正在下载」
				var xfers []dav.Transfer
				for _, v := range vs {
					if v.davfs == nil {
						continue
					}
					for _, t := range v.davfs.Transfers() {
						t.Path = v.name + ":" + t.Path
						xfers = append(xfers, t)
					}
				}
				// 上传队列：面板显示排队/重试中的上传，避免用户以为没反应
				var queued []queue.Item
				for _, v := range vs {
					if v.davfs == nil {
						continue
					}
					q := v.davfs.Queue()
					if q == nil {
						continue
					}
					for _, it := range q.Snapshot() {
						it.Name = v.name + ":" + it.Name
						queued = append(queued, it)
					}
				}
				return map[string]any{
					"state": st, "state_code": code, "auth": auth, "volumes": list,
					"transfers": xfers, "queue": queued,
				}, nil
			},
			Sync: func(ctx context.Context) (any, error) {
				volMu.Lock()
				vs := append([]*trayVolume(nil), volumes...)
				volMu.Unlock()
				out := make([]map[string]any, 0, len(vs))
				for _, v := range vs {
					v.mu.Lock()
					res, err := tgsync.SyncChannel(ctx, v.idx, v.differ, v.store, 100)
					v.mu.Unlock()
					if err != nil {
						out = append(out, map[string]any{"name": v.name, "error": err.Error()})
						continue
					}
					out = append(out, map[string]any{
						"name": v.name, "pulled": res.Pulled, "deleted": res.Deleted, "pts": res.Pts,
					})
				}
				return map[string]any{"results": out}, nil
			},
			CreateVolume: func(ctx context.Context, name, title, point string) (any, error) {
				bn := getBotUsername()
				if bn == "" {
					return nil, fmt.Errorf("%s", i18n.T("err.bot_not_ready"))
				}
				// 建频道/拉 bot 只有 user 会话能做：这里临时开一条 user 连接
				uc := tgclient.New(cfg.Telegram.APIID, apiHash,
					&sessionstore.Encrypted{Path: "secrets/user.session", Key: key},
					tgclient.Options{})
				var res volume.Result
				rerr := uc.Run(ctx, func(ctx context.Context) error {
					r, cerr := volume.Create(ctx, uc.API(), volume.Request{
						Name: name, Title: title, BotUsername: bn,
					})
					res = r
					return cerr
				})
				if rerr != nil {
					if tgclient.IsAuthLost(rerr) {
						return nil, fmt.Errorf("%s: %w", i18n.T("err.auth_lost_hint"), rerr)
					}
					return nil, rerr
				}
				vol := config.Volume{
					Name:    res.Name,
					ChatID:  res.ChannelID,
					Point:   strings.TrimSpace(point),
					Enabled: true,
				}
				if aerr := config.AppendVolume(cfgPath, vol); aerr != nil {
					return nil, aerr
				}
				out := map[string]any{
					"name":             res.Name,
					"channel_id":       res.ChannelID,
					"chat_id":          res.ChatID,
					"muted":            res.Muted,
					"restart_required": true,
				}
				if res.MuteErr != nil {
					out["mute_error"] = res.MuteErr.Error()
				}
				return out, nil
			},
			// 删卷：删 Telegram 频道 → 移出配置 → 删本地索引
			DeleteVolume: func(ctx context.Context, name string) error {
				cur, cerr := config.Load(cfgPath)
				if cerr != nil {
					return cerr
				}
				var target config.Volume
				found := false
				for _, v := range cur.Volumes {
					if strings.EqualFold(v.Name, name) {
						target, found = v, true
						break
					}
				}
				if !found {
					return config.ErrVolumeNotFound
				}
				uc := tgclient.New(cfg.Telegram.APIID, apiHash,
					&sessionstore.Encrypted{Path: "secrets/user.session", Key: key},
					tgclient.Options{})
				rerr := uc.Run(ctx, func(ctx context.Context) error {
					res, e := channel.Resolve(ctx, uc.API(), target.ChatID)
					if e != nil {
						return e
					}
					return channel.Delete(ctx, uc.API(), res)
				})
				if rerr != nil {
					return rerr
				}
				if rerr := config.RemoveVolume(cfgPath, name); rerr != nil {
					return rerr
				}
				for _, suffix := range []string{"", "-wal", "-shm"} {
					_ = os.Remove(datadir.Path("index-" + name + ".db" + suffix))
				}
				return nil
			},
			// 添加已有频道：bot 只读校验频道可见与发言权，然后写配置
			AttachVolume: func(ctx context.Context, name string, chatID int64) (any, error) {
				res, aerr := volume.Attach(ctx, client.API(), volume.AttachRequest{Name: name, ChatID: chatID})
				if aerr != nil {
					return nil, aerr
				}
				vol := config.Volume{
					Name:    res.Name,
					ChatID:  res.ChannelID,
					Enabled: true,
				}
				if werr := config.AppendVolume(cfgPath, vol); werr != nil {
					return nil, werr
				}
				return map[string]any{
					"name":       res.Name,
					"channel_id": res.ChannelID,
					"chat_id":    res.ChatID,
					"title":      res.Title,
				}, nil
			},
			// 列频道：getDialogs 只有 user 会话能做，复用装配根开临时连接
			Channels: func(ctx context.Context) ([]channel.Item, error) {
				return app.New(cfg, app.Options{}).Channels(ctx)
			},
			// 文件管理：复用挂载时创建的 WebDAV 后端，不加额外连接
			Files: func(name string) (fm.Files, bool) {
				volMu.Lock()
				defer volMu.Unlock()
				for _, v := range volumes {
					if v.name == name && v.fmgr != nil {
						return v.fmgr, true
					}
				}
				return nil, false
			},
			Volumes: func() []string {
				volMu.Lock()
				defer volMu.Unlock()
				names := make([]string, 0, len(volumes))
				for _, v := range volumes {
					if v.fmgr != nil {
						names = append(names, v.name)
					}
				}
				return names
			},
			// 卷用量：面板卷标签直接显示文件数与占用
			Stats: func(name string) (int64, int64, bool) {
				volMu.Lock()
				vs := append([]*trayVolume(nil), volumes...)
				volMu.Unlock()
				for _, v := range vs {
					if v.name != name {
						continue
					}
					files, ferr := v.idx.CountFiles(context.Background())
					if ferr != nil {
						return 0, 0, false
					}
					used, uerr := v.idx.Used(context.Background())
					if uerr != nil {
						return 0, 0, false
					}
					return int64(files), used, true
				}
				return 0, 0, false
			},
			// 分片上传的临时文件与 WebDAV 写路径共用同一目录，统一按时间清扫
			TempDir: datadir.Path("tmp"),
			// 挂盘开关：与托盘菜单共用同一套启停逻辑
			Mount: &web.MountControl{
				Status: davState,
				Enable: func() (web.MountState, error) {
					err := setDAV(true)
					return davState(), err
				},
				Disable: func() (web.MountState, error) {
					err := setDAV(false)
					return davState(), err
				},
			},
			// 事件推送：面板保存配置/切换挂载后，托盘立刻同步
			Bus: bus,
		}
		return web.NewHandler(cfgPath, webOpts)
	}

	// 挂载一个卷；挂载点为空表示跳过
	mountOne := func(name string, channelID int64, mp string) {
		if mp == "" {
			return
		}
		api, closePool, perr := client.PooledAPI(cfg.Mount.DownloadConnections())
		if perr != nil {
			fmt.Fprintf(os.Stderr, "%s\n", i18n.T("tray.mount.failed", name, perr))
			return
		}
		vctx, vcancel := context.WithCancel(rootCtx)
		// 索引、存储、WebDAV 语义与文件管理统一由装配根给出
		vf, oerr := app.New(cfg, app.Options{Bus: bus}).OpenVolume(vctx, name, api)
		if oerr != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", i18n.T("log.index_open_failed"), oerr)
			vcancel()
			closePool()
			return
		}
		idx, store, davfs, fmgr := vf.Index, vf.Store, vf.FS, vf.Files
		differ := &tgsync.TelegramChannelDiffer{API: client.API(), ChannelID: channelID}
		tv := &trayVolume{name: name, idx: idx, davfs: davfs, fmgr: fmgr, store: store, differ: differ}

		volMu.Lock()
		volumes = append(volumes, tv)
		volMu.Unlock()

		var vwg sync.WaitGroup
		// 负数表示默认关闭；协程仍然常驻，托盘菜单打开后下一轮即可同步
		interval := SyncInterval(cfg.Mount.SyncSeconds)
		if interval <= 0 {
			interval = SyncInterval(0)
		}
		vwg.Go(func() {
			AutoSyncWhile(vctx, StatusNotifier(bus), idx, differ, store, interval, &tv.mu, syncSwitch.On)
		})

		mu.Lock()
		cancels = append(cancels, vcancel)
		mu.Unlock()

		// 卷常驻到退出：FUSE 挂载已移除，这里不再有内核挂载点需要等待
		wg.Go(func() {
			<-vctx.Done()
			vwg.Wait()
			_ = vf.Close()
			closePool()
			volMu.Lock()
			for i, v := range volumes {
				if v == tv {
					volumes = append(volumes[:i], volumes[i+1:]...)
					break
				}
			}
			volMu.Unlock()
		})
	}

	// mountAll 在连接就绪后执行一次：卷初始化需要已登录的 MTProto 客户端
	// （PooledAPI 在未初始化时会空指针），所以不能提前调用。
	mountAll := func() {
		if len(cfg.Volumes) == 0 {
			mountOne("mytgfs", cfg.Telegram.ChannelID, point)
		} else {
			for _, v := range cfg.Volumes {
				if !v.Enabled {
					continue
				}
				mp := v.Point
				if mp == "" {
					mp = point
				}
				mountOne(v.Name, v.ChatID, mp)
			}
		}
		// 挂盘服务要在卷就绪之后才能启动；失败不影响挂载与托盘
		if cfg.WebDAV.Enabled {
			if derr := startDAV(); derr != nil {
				fmt.Fprintf(os.Stderr, "%v\n", derr)
			}
		}
	}
	mountOnce := sync.OnceFunc(mountAll)

	// 连接循环：断网由 gotd 内部无限重连；Run 只在永久错误或 ctx 取消时返回
	wg.Go(func() {
		rerr := client.Run(rootCtx, func(ctx context.Context) error {
			// 连接就绪后读一次 bot 用户名，建卷时用它拉 bot 进新频道
			if self, serr := client.Self(ctx); serr == nil {
				setBotUsername(self.Username)
			}
			mountOnce()
			<-ctx.Done()
			return nil
		})
		if rootCtx.Err() != nil {
			return
		}
		if tgclient.IsAuthLost(rerr) {
			authLost.Store(true)
			setState("tray.state.auth_lost")
			fmt.Fprintf(os.Stderr, "%s: %v\n", i18n.T("log.auth_lost"), rerr)
			return
		}
		// 其它永久错误（app id 无效等）无法自愈，保持托盘可退出
		setState("tray.state.reconnecting")
		fmt.Fprintf(os.Stderr, "%s: %v\n", i18n.T("log.conn_closed"), rerr)
	})

	// 开机自启：状态以系统注册项为准，默认关闭
	autoOn, aerr := autostart.Enabled()
	if aerr != nil {
		autoOn = false
		fmt.Fprintf(os.Stderr, "%s: %v\n", i18n.T("log.autostart_read_failed"), aerr)
	}
	exePath, exeErr := os.Executable()

	// ---- 托盘菜单 ----
	// 状态行只做展示；开关项点击后立即落盘，并回写勾选状态
	menu := wailsApp.NewMenu()
	statusItem = menu.Add(connText)
	statusItem.SetEnabled(false)
	menu.AddSeparator()
	menu.Add(i18n.T("tray.menu.open")).OnClick(func(*application.Context) {
		win.Show()
		win.Focus()
	})
	menu.AddSeparator()

	autoItem = menu.AddCheckbox(i18n.T("tray.menu.autostart"), autoOn)
	autoItem.OnClick(func(*application.Context) {
		on := !autoOn
		if exeErr != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", i18n.T("log.autostart_toggle_failed"), exeErr)
			return
		}
		var terr error
		if on {
			terr = autostart.Enable(exePath)
		} else {
			terr = autostart.Disable()
		}
		if terr != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", i18n.T("log.autostart_toggle_failed"), terr)
			return
		}
		autoOn = on
		autoItem.SetChecked(on)
	})

	davItem = menu.AddCheckbox(i18n.T("tray.menu.webdav"), davState().Enabled)
	davItem.OnClick(func(*application.Context) {
		on := !davState().Enabled
		if derr := setDAV(on); derr != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", i18n.T("log.webdav_toggle_failed"), derr)
			return
		}
		davItem.SetChecked(on)
	})

	syncItem = menu.AddCheckbox(i18n.T("tray.menu.autosync"), syncSwitch.On())
	syncItem.OnClick(func(*application.Context) {
		on := !syncSwitch.On()
		// 写回配置：打开用默认 15s，关闭写 -1
		secs := 0
		if !on {
			secs = -1
		}
		if werr := config.SetKey(cfgPath, "mount.sync_seconds", strconv.Itoa(secs)); werr != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", i18n.T("log.autosync_toggle_failed"), werr)
			return
		}
		syncSwitch.Set(on)
		syncItem.SetChecked(on)
	})

	menu.AddSeparator()
	menu.Add(i18n.T("tray.menu.quit")).OnClick(func(*application.Context) {
		setState("tray.state.unmounted")
		// 先停 WebDAV 再卸卷，避免客户端读到已关闭的索引
		_ = stopDAV()
		unmountAll()
		rootCancel()
		wailsApp.Quit()
	})

	sysTray.SetMenu(menu)
	sysTray.OnClick(func() {
		win.Show()
		win.Focus()
	})

	// 托盘悬浮提示：状态 + 每卷占用 + 正在跑的任务，每 5 秒刷新
	tooltipText := func() string {
		stateMu.Lock()
		st := connText
		stateMu.Unlock()
		lines := []string{st}
		volMu.Lock()
		vs := append([]*trayVolume(nil), volumes...)
		volMu.Unlock()
		var downloads, queued int
		for _, v := range vs {
			if children, lerr := v.idx.Children(rootCtx, 0); lerr == nil {
				used, _ := v.idx.Used(rootCtx)
				lines = append(lines, fmt.Sprintf(i18n.T("tray.tip.vol"), v.name, len(children), humanBytes(used)))
			}
			if v.davfs == nil {
				continue
			}
			downloads += len(v.davfs.Transfers())
			if q := v.davfs.Queue(); q != nil {
				queued += q.Pending()
			}
		}
		if downloads > 0 || queued > 0 {
			lines = append(lines, fmt.Sprintf(i18n.T("tray.tip.tasks"), downloads, queued))
		}
		if syncSwitch.On() {
			lines = append(lines, i18n.T("tray.tip.sync_on"))
		} else {
			lines = append(lines, i18n.T("tray.tip.sync_off"))
		}
		return strings.Join(lines, "\n")
	}
	wg.Go(func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			uiUpdate(func() { sysTray.SetTooltip(tooltipText()) })
			select {
			case <-rootCtx.Done():
				return
			case <-ticker.C:
			}
		}
	})

	// 主循环起来之后才能安全操作窗口与托盘对象
	wailsApp.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		ready.Store(true)
		sysTray.SetTooltip(tooltipText())
	})

	// 面板路由直接挂到 webview：不监听端口、不开浏览器
	setHandler(buildPanel())

	if err := wailsApp.Run(); err != nil {
		return err
	}

	// 窗口退出后收尾：停挂盘服务、卸载卷、取消后台任务
	_ = stopDAV()
	unmountAll()
	rootCancel()
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}
	return nil
}

// humanBytes 把字节数格式化成 1 位小数的可读单位。
//
// 输入：n 字节数
// 输出：如 "159.1 MB"
// 预期：1024 进制，与面板的占用显示一致
func humanBytes(n int64) string {
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

// stateCode 把托盘状态语言包键映射成机器可读代码。
//
// 输入：key 形如 tray.state.connected
// 输出：online / connecting / reconnecting / auth_lost / unmounted / starting
// 预期：面板按代码决定状态条颜色，不再匹配翻译后的文本
func stateCode(key string) string {
	switch key {
	case "tray.state.connected":
		return "online"
	case "tray.state.connecting":
		return "connecting"
	case "tray.state.reconnecting":
		return "reconnecting"
	case "tray.state.auth_lost":
		return "auth_lost"
	case "tray.state.unmounted":
		return "unmounted"
	default:
		return "starting"
	}
}
