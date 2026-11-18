package trayapp

// 托盘主管进程：挂载卷、自动增量同步、按需开关 Web 面板、托盘菜单。
//
// 这个包不依赖命令行参数解析，方便 CLI 的 `tray` 子命令和
// GUI 子系统的 `mytgfs-tray.exe` 共用同一套逻辑。

import (
	"cmp"
	"context"
	"fmt"
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
	"mytgfs/internal/fm"
	"mytgfs/internal/i18n"
	"mytgfs/internal/index"
	"mytgfs/internal/logging"
	"mytgfs/internal/push"
	"mytgfs/internal/queue"
	"mytgfs/internal/sessionstore"
	tgsync "mytgfs/internal/sync"
	"mytgfs/internal/telegramstore"
	"mytgfs/internal/tgclient"
	"mytgfs/internal/uploadcache"
	"mytgfs/internal/vfs"
	"mytgfs/internal/volume"
	"mytgfs/internal/web"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Options 是托盘启动参数。
type Options struct {
	ConfigPath string // 配置文件路径，空则 config.toml
	// SingleInstance 为 true 时启用进程级单实例锁；第二个实例只唤起已有窗口后退出
	SingleInstance bool
	// SetupAllowed 为 true 时，缺 API 凭证不再直接报错，只开登录面板；
	// 安卓没有命令行先跑 auth，首次启动靠面板填凭证与 bot token
	SetupAllowed bool
}

// volumeMenuLabel 返回托盘卷入口的文字。
//
// 输入：name 卷名；used 已用字节
// 输出：形如 "demo · 159.1 MB"
// 预期：托盘一眼看清是哪个盘、占多少

func Run(opts Options) error {
	cfgPath := cmp.Or(opts.ConfigPath, config.DefaultConfigPath)

	cfg, err := config.Load(cfgPath)
	if err != nil {
		if opts.SetupAllowed {
			return runSetupPanel(cfgPath)
		}
		return fmt.Errorf("%s: %w", i18n.T("err.config_read"), err)
	}
	apiHash := cfg.APIHash()
	if apiHash == "" {
		if opts.SetupAllowed {
			return runSetupPanel(cfgPath)
		}
		return fmt.Errorf("%s", i18n.T("err.missing_api_hash"))
	}
	key, err := crypto.LoadOrCreateKey(app.DefaultKeyPath)
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
	// 面板路由的唯一出口：静态资源与安卓桥共用同一份可热替换的 handler
	assets := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hmu.RLock()
		h := inner
		hmu.RUnlock()
		h.ServeHTTP(w, r)
	})

	// 单实例锁：第二个实例启动时唤起已有窗口后自行退出；CLI 子命令不经过这里
	var win *application.WebviewWindow
	var single *application.SingleInstanceOptions
	if opts.SingleInstance {
		single = &application.SingleInstanceOptions{
			UniqueID: "com.mytgfs.tray",
			OnSecondInstanceLaunch: func(application.SecondInstanceData) {
				if win != nil {
					win.Show()
					win.Focus()
				}
			},
		}
	}
	wailsOpts := shellOptions(assets)
	wailsOpts.SingleInstance = single
	wailsOpts.Windows = application.WindowsOptions{AdditionalBrowserArgs: e2eBrowserArgs()}
	wailsApp := application.New(wailsOpts)
	win = wailsApp.Window.NewWithOptions(application.WebviewWindowOptions{
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
	// 运行时就绪回调在 Hub 创建之前注册，用变量延迟绑定发布动作
	var onRuntimeReady func()
	// WebView 运行时就绪后才可安全 Emit：此前 Hub 发出的 status 事件没有接收者，
	// 首屏会永久停在挂载前的空状态。就绪时补推一次，让面板重取完整快照。
	win.OnWindowEvent(events.Common.WindowRuntimeReady, func(*application.WindowEvent) {
		if onRuntimeReady != nil {
			onRuntimeReady()
		}
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

	var statusItem *application.MenuItem
	// rebuildMenu 由下方菜单构建逻辑赋值；卷挂载/卸载后重建卷入口
	rebuildMenu := func() {}

	// trayVolume 是一个已挂载卷的运行时句柄，供状态展示与手动同步使用。
	type trayVolume struct {
		vf     *app.VolumeFS
		name   string
		idx    *index.Index
		fsys   *vfs.FileSystem
		fmgr   *fm.Manager
		store  *telegramstore.Store
		differ tgsync.ChannelDiffer
		mu     sync.Mutex // 自动同步与手动同步互斥
		// cancel 停掉该卷的同步协程；done 在清理协程关完索引与连接池后关闭。
		// 删卷时先摘列表再 cancel 并等 done，之后才能安全删除索引文件
		//（Windows 下打开中的 SQLite 文件删除会失败）。
		cancel context.CancelFunc
		done   chan struct{}
	}
	// reg 是运行中卷的唯一账本：列卷/开卷/删卷共用，删卷时同步摘除
	reg := app.NewVolumeRegistry[*trayVolume]()

	// 自动同步开关：每轮读配置，面板改设置后下一轮生效
	syncOn := SyncEnabled(cfgPath, cfg.Mount.SyncOn())

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

	unmountAll := func() {
		mu.Lock()
		cs := cancels
		cancels = nil
		mu.Unlock()
		for _, c := range cs {
			c()
		}
	}

	// pushHub 是面板推送的唯一入口：配置/状态变更立刻推给 WebView
	pushHub := push.NewHub(rootCtx)
	defer pushHub.Close()
	onRuntimeReady = func() { pushHub.Publish(push.TopicStatus) }

	// 装配根复用：开卷与写频道头像都走同一套配置/会话
	appRoot := app.New(cfg, app.Options{Push: pushHub, ConfigPath: cfgPath})

	// 封面服务在装配层创建：挂载循环要在 pts 变化时失效它的缓存
	coverProvider := app.NewCoverProvider(appRoot, func(name string) (*app.VolumeFS, bool) {
		v, ok := reg.Get(name)
		if !ok || v.vf == nil {
			return nil, false
		}
		return v.vf, true
	}, datadir.Path("thumbs"))

	client := tgclient.New(cfg.Telegram.APIID, apiHash,
		&sessionstore.Encrypted{Path: app.DefaultSessionPath, Key: key},
		tgclient.Options{
			// 首次运行（手机、全新安装）只有 token 文件没有会话：
			// 让 Run 自己登录，否则拿到的 auth key 未登记，所有 RPC 都是 401
			BotToken: cfg.BotToken(),
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
				logging.Warn("log.session_invalid", err)
				return err
			},
		})

	// 面板路由：桌面壳直接挂在 webview 上，不监听端口
	// reconcileVolumes 让运行时卷表对齐到配置文件（建卷/attach/删卷共用收尾），
	// 定义在 mountOne 之后；面板请求发生时已赋值
	var reconcileVolumes func()
	buildPanel := func() http.Handler {
		webOpts := web.Options{
			Auth: authsvc.New(authsvc.Paths{ConfigPath: cfgPath}),
			// 桌面壳里的自启开关与托盘菜单写同一处注册项
			Autostart: autostart.Control{},
			// 下载目录：留空时由面板按 data/downloads 解析
			DownloadsDir: cfg.Downloads.EffectiveDir(),
			Status: func(ctx context.Context) (any, error) {
				stateMu.Lock()
				st := connText
				code := stateCode(connKey)
				stateMu.Unlock()
				vs := reg.Snapshot()
				list := make([]map[string]any, 0, len(vs))
				var xfers []vfs.Transfer
				var queued []queue.Item
				for _, v := range vs {
					if v.vf == nil {
						continue
					}
					snap, err := v.vf.Snapshot(ctx)
					if err != nil {
						list = append(list, map[string]any{"name": v.name, "error": err.Error()})
						continue
					}
					list = append(list, map[string]any{
						"name": snap.Name, "entries": snap.Entries, "used": snap.Used,
					})
					xfers = append(xfers, snap.Transfers...)
					queued = append(queued, snap.Queue...)
				}
				auth := "ok"
				if authLost.Load() {
					auth = "lost"
				}
				return map[string]any{
					"state": st, "state_code": code, "auth": auth, "volumes": list,
					"transfers": xfers, "queue": queued,
				}, nil
			},
			Sync: func(ctx context.Context) (any, error) {
				vs := reg.Snapshot()
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
			// 配置变更唯一入口：写盘与内存副本一步完成
			MutateConfig: appRoot.MutateConfig,
			// 设置保存后立即应用到运行中的卷：并发数、读块、日志级别即时生效
			OnSettingsChanged: func(cur config.Config) {
				for _, tv := range reg.Snapshot() {
					tv.vf.Store.SetParallel(cur.Mount.DownloadConnections())
					tv.vf.Store.SetUploadParallel(cur.Mount.UploadParallelism())
					tv.vf.FS.SetChunk(cur.Mount.ReadChunk())
				}
				logging.SetLevel(cur.Log.Level)
				pushHub.Publish(push.TopicStatus)
			},
			// 建卷：重名检查、读 bot 用户名、开 user 连接、写配置都在 app 装配根
			CreateVolume: func(ctx context.Context, name, title string) (any, error) {
				res, err := appRoot.CreateVolume(ctx, name, title, "")
				if err != nil {
					return nil, err
				}
				// 建完立即挂载并推送：新卷不出现在面板就是这类前后端脱节
				reconcileVolumes()
				return res, nil
			},
			// 删卷：删频道 → 移出配置 → 清缓存与索引（实现在 app 装配根）
			DeleteVolume: func(ctx context.Context, name string) error {
				res, derr := appRoot.DeleteVolume(ctx, name)
				if derr != nil {
					return derr
				}
				// 运行时对齐配置：停同步、关索引、摘注册表，一次完成（内部会等卸载完再推送）
				reconcileVolumes()
				// 磁盘缓存已由删卷流程清掉，这里只清进程内的头像缓存
				coverProvider.Forget(volume.NormalizeChatID(res.ChannelID))
				return nil
			},
			// 挂载已有频道：只读校验与写配置都在 app 装配根
			AttachVolume: func(ctx context.Context, name string, chatID int64) (any, error) {
				res, err := appRoot.AttachVolume(ctx, name, chatID)
				if err != nil {
					return nil, err
				}
				reconcileVolumes()
				return res, nil
			},
			// 列频道：getDialogs 只有 user 会话能做，复用装配根开临时连接
			Channels: func(ctx context.Context) ([]channel.Item, error) {
				return app.New(cfg, app.Options{}).Channels(ctx)
			},
			// 文件管理：复用打开卷时创建的文件系统后端，不加额外连接
			Files: func(name string) (fm.Files, bool) {
				v, ok := reg.Get(name)
				if !ok || v.fmgr == nil {
					return nil, false
				}
				return v.fmgr, true
			},
			Volumes: func() []string {
				names := make([]string, 0, 4)
				for _, v := range reg.Snapshot() {
					if v.fmgr != nil {
						names = append(names, v.name)
					}
				}
				return names
			},
			// 卷用量：面板卷标签直接显示文件数与占用
			Stats: func(name string) (int64, int64, bool) {
				v, ok := reg.Get(name)
				if !ok || v.idx == nil {
					return 0, 0, false
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
			},
			// 分片上传的临时文件统一按时间清扫
			TempDir: datadir.Path("tmp"),
			// 事件推送：面板保存配置/文件变化后，托盘立刻同步
			Push: pushHub,
			// 清空已结束的队列记录：面板按钮直接落到各卷的持久化表
			ClearQueue: func(ctx context.Context) (int64, error) {
				var total int64
				for _, v := range reg.Snapshot() {
					n, cerr := v.vf.ClearQueue(ctx)
					if cerr != nil {
						return total, cerr
					}
					total += n
				}
				return total, nil
			},
			// 取消单条任务：按卷名找到队列；卷已不在时按「记录不存在」处理
			CancelQueue: func(ctx context.Context, volume string, id int64) error {
				v, ok := reg.Get(volume)
				if !ok {
					return queue.ErrJobNotFound
				}
				return v.vf.CancelQueue(ctx, id)
			},
			// 封面就是频道头像：按卷名找到对应 Store；字节缓存落 thumbs 目录
			// 封面就是频道头像：复用装配层的封面服务
			Cover: coverProvider,
			// 重建索引：user 会话全量拉历史后覆盖本地节点
			Rebuild: func(ctx context.Context, name string) (any, error) {
				v, ok := reg.Get(name)
				if !ok || v.vf == nil {
					return nil, app.ErrVolumeNotFound{Name: name}
				}
				v.mu.Lock()
				n, rerr := appRoot.RebuildIndex(ctx, v.idx, volume.NormalizeChatID(v.vf.Volume.ChatID))
				v.mu.Unlock()
				if rerr != nil {
					return nil, rerr
				}
				return map[string]any{"files": n}, nil
			},
			// 视频缩略图：Telegram 已生成的缩略图，按卷转发
			VideoThumb: func(ctx context.Context, vol, path string) ([]byte, string, error) {
				v, ok := reg.Get(vol)
				if !ok || v.vf == nil {
					return nil, "", os.ErrNotExist
				}
				return v.vf.VideoThumb(ctx, path)
			},
		}
		return web.NewHandler(cfgPath, webOpts)
	}

	// openVolume 把一个卷接进托盘：索引、存储与文件管理都由装配根给出
	mountOne := func(name string, channelID int64) {
		api, closePool, perr := client.PooledAPI(cfg.Mount.DownloadConnections())
		if perr != nil {
			logging.Warn("tray.volume.open_failed", perr, name)
			pushHub.Publish(push.TopicStatus)
			return
		}
		vctx, vcancel := context.WithCancel(rootCtx)
		// 索引、存储、文件系统语义与文件管理统一由装配根给出
		vf, oerr := appRoot.OpenVolume(vctx, name, api)
		if oerr != nil {
			logging.Warn("log.index_open_failed", oerr)
			vcancel()
			closePool()
			pushHub.Publish(push.TopicStatus)
			return
		}
		idx, store, fsys, fmgr := vf.Index, vf.Store, vf.FS, vf.Files
		differ := &tgsync.TelegramChannelDiffer{API: client.API(), ChannelID: channelID}
		tv := &trayVolume{vf: vf, name: name, idx: idx, fsys: fsys, fmgr: fmgr, store: store, differ: differ, cancel: vcancel, done: make(chan struct{})}

		reg.Put(name, tv)
		// 卷已就绪：推一条 status，面板连上后立即拿到真实卷列表，无需定时重试
		pushHub.Publish(push.TopicStatus)
		rebuildMenu()

		var vwg sync.WaitGroup
		// 负数表示默认关闭；协程仍然常驻，托盘菜单打开后下一轮即可同步
		// 挂载时现读配置：设置里改过间隔的新卷立即用新值
		interval := SyncInterval(appRoot.Config().Mount.SyncSeconds)
		if interval <= 0 {
			interval = SyncInterval(0)
		}
		vwg.Go(func() {
			AutoSyncWhile(vctx, StatusNotifier(pushHub), idx, differ, store, interval, &tv.mu, syncOn, func() {
				// pts 推进＝频道有更新：头像缓存失效重建，事件驱动不轮询
				coverProvider.InvalidateVolume(name)
			})
		})

		mu.Lock()
		cancels = append(cancels, vcancel)
		mu.Unlock()

		// 卷常驻到退出或被删卷：vctx 取消后清理资源并把自己从账本摘除
		wg.Go(func() {
			<-vctx.Done()
			vwg.Wait()
			_ = vf.Close()
			closePool()
			reg.Delete(name)
			close(tv.done)
			rebuildMenu()
		})
	}

	// isMounted 判断卷是否已经在托盘里。
	isMounted := func(name string) bool {
		_, ok := reg.Get(name)
		return ok
	}

	// reconcileVolumes 让运行时卷表对齐到**配置文件**（不是内存快照）：
	// 配置里启用而未挂载的 → mountOne 挂载；挂载了而配置里没有/停用的 → 停掉。
	// 建卷/attach/删卷等一切配置变更都以调用它收尾；周期对账也让
	// CLI 建卷、手编 config.toml 等外部改动自动被收编，不需要重启
	// reconcileMu 串行化对账：菜单回调与周期对账可能同时进来，
	// 并发 mountOne 会把同一个卷挂两次（两份队列抢同一个索引库）
	var reconcileMu sync.Mutex
	reconcileVolumes = func() {
		reconcileMu.Lock()
		defer reconcileMu.Unlock()
		cur, err := config.Load(cfgPath)
		if err != nil {
			logging.Warn("log.volume_warn", err)
			return
		}
		appRoot.SetConfig(cur)
		mount, unmount := app.VolumeSync(cur.Volumes, reg.Names())
		for _, v := range mount {
			mountOne(v.Name, v.ChatID)
		}
		for _, name := range unmount {
			tv, ok := reg.Get(name)
			if !ok {
				continue
			}
			tv.cancel() // 生命周期协程负责关索引、摘注册表、close(done)
			<-tv.done
		}
		// 旧单卷形态：配置里没有 [[volumes]] 时用 telegram.channel_id 兜底
		if len(cur.Volumes) == 0 && cur.Telegram.ChannelID != 0 && !isMounted(app.DefaultVolumeName) {
			mountOne(app.DefaultVolumeName, cur.Telegram.ChannelID)
		}
		pushHub.Publish(push.TopicStatus)
	}

	// mountAll 对齐到最新配置（面板建卷/删卷、外部编辑都会被收编）。
	mountAll := func() {
		reconcileVolumes()
	}
	// 断点断片超过一天就清：Telegram 服务端临时区约一天过期，续不上的断点只会堆积
	uploadcache.Sweep(datadir.Path("uploads"), 24*time.Hour)
	// allMounted 判断配置里所有启用的卷是否都已挂载。
	allMounted := func() bool {
		cur := appRoot.Config()
		if len(cur.Volumes) == 0 {
			return isMounted(app.DefaultVolumeName)
		}
		for _, v := range cur.Volumes {
			if v.Enabled && !isMounted(v.Name) {
				return false
			}
		}
		return true
	}
	// mountUntilAll 反复重试直到全部卷挂载成功或进程退出。
	mountUntilAll := func(ctx context.Context) {
		wait := 2 * time.Second
		for {
			mountAll()
			if allMounted() || ctx.Err() != nil {
				return
			}
			logging.Warn("log.mount_retry", nil)
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				return
			}
			wait = min(wait*2, 30*time.Second)
		}
	}

	// 连接循环：断网由 gotd 内部无限重连；Run 只在永久错误或 ctx 取消时返回
	wg.Go(func() {
		rerr := client.Run(rootCtx, func(ctx context.Context) error {
			mountUntilAll(ctx)
			<-ctx.Done()
			return nil
		})
		if rootCtx.Err() != nil {
			return
		}
		if tgclient.IsAuthLost(rerr) {
			authLost.Store(true)
			setState("tray.state.auth_lost")
			logging.Warn("log.auth_lost", rerr)
			return
		}
		// 其它永久错误（app id 无效等）无法自愈，保持托盘可退出
		setState("tray.state.reconnecting")
		logging.Warn("log.conn_closed", rerr)
	})

	// ---- 托盘菜单 ----
	// 开关项已移入面板设置；托盘只保留状态、转卷快捷入口与退出。
	openVolume := func(name string) {
		win.Show()
		win.Focus()
		win.SetURL(volumeURL(name))
	}
	buildMenu := func() *application.Menu {
		menu := wailsApp.NewMenu()
		stateMu.Lock()
		text := connText
		stateMu.Unlock()
		statusItem = menu.Add(text)
		statusItem.SetEnabled(false)

		vs := reg.Snapshot()
		if len(vs) > 0 {
			menu.AddSeparator()
			// 每卷一项：名字 + 已用容量，点击直接打开该卷文件页
			for _, v := range vs {
				used, _ := v.idx.Used(rootCtx)
				menu.Add(volumeMenuLabel(v.name, used)).OnClick(func(*application.Context) {
					openVolume(v.name)
				})
			}
		}

		menu.AddSeparator()
		menu.Add(i18n.T("tray.menu.open")).OnClick(func(*application.Context) {
			win.Show()
			win.Focus()
		})
		menu.AddSeparator()
		menu.Add(i18n.T("tray.menu.quit")).OnClick(func(*application.Context) {
			// 先停卷协程再退出，避免关到一半的索引
			unmountAll()
			rootCancel()
			wailsApp.Quit()
		})
		return menu
	}
	rebuildMenu = func() {
		uiUpdate(func() { sysTray.SetMenu(buildMenu()) })
	}

	sysTray.SetMenu(buildMenu())
	sysTray.OnClick(func() {
		win.Show()
		win.Focus()
	})

	// 定期从配置文件对账：CLI 建卷、手编 config.toml 的外部改动自动被收编
	wg.Go(func() {
		interval := SyncInterval(appRoot.Config().Mount.SyncSeconds)
		if interval <= 0 {
			interval = SyncInterval(0)
		}
		for {
			select {
			case <-rootCtx.Done():
				return
			case <-time.Tick(interval):
				reconcileVolumes()
			}
		}
	})

	// 托盘悬浮提示：状态 + 每卷占用 + 正在跑的任务，每 5 秒刷新
	tooltipText := func() string {
		stateMu.Lock()
		st := connText
		stateMu.Unlock()
		lines := []string{st}
		vs := reg.Snapshot()
		var downloads, queued int
		for _, v := range vs {
			if children, lerr := v.idx.Children(rootCtx, 0); lerr == nil {
				used, _ := v.idx.Used(rootCtx)
				lines = append(lines, fmt.Sprintf(i18n.T("tray.tip.vol"), v.name, len(children), humanBytes(used)))
			}
			if v.fsys == nil {
				continue
			}
			downloads += len(v.fsys.Transfers())
			if q := v.fsys.Queue(); q != nil {
				queued += q.Pending()
			}
		}
		if downloads > 0 || queued > 0 {
			lines = append(lines, fmt.Sprintf(i18n.T("tray.tip.tasks"), downloads, queued))
		}
		return strings.Join(lines, "\n")
	}
	wg.Go(func() {
		tick := time.Tick(5 * time.Second)
		for {
			uiUpdate(func() { sysTray.SetTooltip(tooltipText()) })
			select {
			case <-rootCtx.Done():
				return
			case <-tick:
			}
		}
	})

	// 主循环起来之后才能安全操作窗口与托盘对象
	wailsApp.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		ready.Store(true)
		// 安卓没有托盘：用前台服务把进程挂在后台，息屏后同步/上传才不会被回收
		startForeground()
		sysTray.SetMenu(buildMenu())
		sysTray.SetTooltip(tooltipText())
	})

	// 面板路由直接挂到 webview：不监听端口、不开浏览器
	setHandler(buildPanel())

	// 桌面传输通道：Hub 统一扇出，这里只把主题翻成 Wails 原生事件名。
	// 网页版在同一个 Hub 上注册 SSE 订阅者，两端共用一份契约。
	unsubscribe := pushHub.Subscribe(func(topic string) {
		wailsApp.Event.Emit(push.DesktopEvent(topic))
	})
	defer unsubscribe()

	if err := wailsApp.Run(); err != nil {
		return err
	}

	// 窗口退出后收尾：卸载卷、取消后台任务
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
