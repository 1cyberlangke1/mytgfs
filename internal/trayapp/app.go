package trayapp

// 托盘主管进程：挂载卷、自动增量同步、按需开关 Web 面板、托盘菜单。
//
// 这个包不依赖命令行参数解析，方便 CLI 的 `tray` 子命令和
// GUI 子系统的 `mytgfs-tray.exe` 共用同一套逻辑。

import (
	"cmp"
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"mytgfs/internal/autostart"
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
	"mytgfs/internal/tray"
	"mytgfs/internal/volume"
	"mytgfs/internal/web"
)

// Options 是托盘启动参数。
type Options struct {
	ConfigPath string // 配置文件路径，空则 config.toml
	Point      string // 挂载点覆盖，空则取配置
	Port       int    // Web 面板端口，0 则 8720
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
func AutoSync(ctx context.Context, notify func(changed, removed []string), idx *index.Index, d tgsync.ChannelDiffer, interval time.Duration, mu *sync.Mutex) {
	AutoSyncWhile(ctx, notify, idx, d, interval, mu, nil)
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
func AutoSyncWhile(ctx context.Context, notify func(changed, removed []string), idx *index.Index, d tgsync.ChannelDiffer, interval time.Duration, mu *sync.Mutex, enabled func() bool) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	syncOnce := func() (tgsync.ChannelSyncResult, error) {
		if mu != nil {
			mu.Lock()
			defer mu.Unlock()
		}
		return tgsync.SyncChannel(ctx, idx, d, 100)
	}
	for {
		if enabled == nil || enabled() {
			res, err := syncOnce()
			if ctx.Err() != nil {
				return
			}
			switch {
			case err != nil:
				fmt.Fprintf(os.Stderr, "自动同步失败: %v\n", err)
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
		return fmt.Errorf("读取配置: %w", err)
	}
	// 端口优先级：命令行 -port > config.toml [web].port > 默认 8720
	port := opts.Port
	if port <= 0 {
		port = cfg.Web.ListenPort()
	}
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
	connText := i18n.T("tray.state.starting")
	setState := func(s string) {
		stateMu.Lock()
		connText = s
		stateMu.Unlock()
		tray.SetStatus(s)
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
				tray.SetWebDAV(davSvc.State().Enabled)
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
					setState(i18n.T("tray.state.connected"))
				case tgclient.ConnConnecting:
					setState(i18n.T("tray.state.connecting"))
				default:
					setState(i18n.T("tray.state.reconnecting"))
				}
			},
			OnAuthLost: func(_ context.Context, err error) error {
				// 会话被吊销只能重新登录；停止 Run，别让连接循环空转
				authLost.Store(true)
				setState(i18n.T("tray.state.auth_lost"))
				fmt.Fprintf(os.Stderr, "会话失效: %v\n", err)
				return err
			},
		})

	// Web 面板：按需启动/关闭，绑定 127.0.0.1
	var webMu sync.Mutex
	var webCancel context.CancelFunc
	var webAddr string

	startWeb := func() string {
		webMu.Lock()
		defer webMu.Unlock()
		if webCancel != nil {
			return webAddr
		}
		addr := fmt.Sprintf("127.0.0.1:%d", port)
		if ln, lerr := net.Listen("tcp", addr); lerr != nil {
			// 端口被占用时退回随机端口，面板仍可用
			if ln2, lerr2 := net.Listen("tcp", "127.0.0.1:0"); lerr2 == nil {
				addr = ln2.Addr().String()
				_ = ln2.Close()
			}
		} else {
			_ = ln.Close()
		}
		ctx, cancel := context.WithCancel(rootCtx)
		webCancel = cancel
		webAddr = addr
		webOpts := web.Options{
			Status: func(ctx context.Context) (any, error) {
				stateMu.Lock()
				st := connText
				stateMu.Unlock()
				volMu.Lock()
				vs := append([]*trayVolume(nil), volumes...)
				volMu.Unlock()
				list := make([]map[string]any, 0, len(vs))
				for _, v := range vs {
					entries, err := v.idx.List(ctx, "/")
					if err != nil {
						list = append(list, map[string]any{"name": v.name, "error": err.Error()})
						continue
					}
					used, _ := v.idx.Used(ctx)
					list = append(list, map[string]any{
						"name": v.name, "entries": len(entries), "used": used,
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
					"state": st, "auth": auth, "volumes": list,
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
					res, err := tgsync.SyncChannel(ctx, v.idx, v.differ, 100)
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
			// 事件推送：面板保存配置/切换挂载后，其它标签页立刻同步
			Bus: bus,
			// PWA：默认启用，配置里可改名/关掉/限制单文件离线大小
			PWA: cfg.PWA,
		}
		go func() {
			if serr := web.ServeWith(ctx, addr, cfgPath, webOpts); serr != nil {
				fmt.Fprintf(os.Stderr, "web: %v\n", serr)
			}
			webMu.Lock()
			if webAddr == addr {
				webCancel = nil
			}
			webMu.Unlock()
		}()
		return addr
	}
	stopWeb := func() {
		webMu.Lock()
		c := webCancel
		webCancel = nil
		webMu.Unlock()
		if c != nil {
			c()
		}
	}

	// 挂载一个卷；挂载点为空表示跳过
	mountOne := func(name string, channelID int64, mp string) {
		if mp == "" {
			return
		}
		idx, oerr := index.Open(datadir.Path("index-" + name + ".db"))
		if oerr != nil {
			fmt.Fprintf(os.Stderr, "打开索引 %s: %v\n", name, oerr)
			return
		}
		// 索引是缓存，丢了用 user 会话按频道历史重建
		if rerr := RebuildIndexIfEmpty(rootCtx, cfg, idx, channelID); rerr != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", i18n.T("tray.mount.failed", name), rerr)
		}
		api, closePool, perr := client.PooledAPI(cfg.Mount.DownloadConnections())
		if perr != nil {
			fmt.Fprintf(os.Stderr, "%s\n", i18n.T("tray.mount.failed", name, perr))
			return
		}
		store := telegramstore.New(api, channelID)
		store.SetParallel(cfg.Mount.DownloadConnections())
		store.SetUploadParallel(cfg.Mount.UploadParallelism())
		vctx, vcancel := context.WithCancel(rootCtx)
		// 面板文件管理与可选 WebDAV 共用同一份 WebDAV 语义后端
		davfs := dav.New(vctx, idx, store, cfg.Mount.ReadChunk())
		// 面板上传与 WebDAV 共用同一队列，批量操作串行提交
		davfs.SetQueue(queue.New(1, 3))
		// 上传临时文件放数据目录，异常中断的残留由 dav 按时间清扫
		davfs.SetTempDir(datadir.Path("tmp"))
		// 重启后按账本补传上次未提交的文件，只处理本程序记下的记录
		if rerr := davfs.ResumePending(); rerr != nil {
			fmt.Fprintf(os.Stderr, "pending resume (%s): %v\n", name, rerr)
		}
		fmgr := fm.New(davfs)
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
			AutoSyncWhile(vctx, nil, idx, differ, interval, &tv.mu, syncSwitch.On)
		})

		mu.Lock()
		cancels = append(cancels, vcancel)
		mu.Unlock()

		// 卷常驻到退出：FUSE 挂载已移除，这里不再有内核挂载点需要等待
		wg.Go(func() {
			<-vctx.Done()
			vwg.Wait()
			_ = idx.Close()
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
			setState(i18n.T("tray.state.auth_lost"))
			fmt.Fprintf(os.Stderr, "凭据失效: %v\n", rerr)
			return
		}
		// 其它永久错误（app id 无效等）无法自愈，保持托盘可退出
		setState(i18n.T("tray.state.reconnecting"))
		fmt.Fprintf(os.Stderr, "Telegram 连接结束: %v\n", rerr)
	})

	// 开机自启：状态以系统注册项为准，默认关闭
	autoOn, aerr := autostart.Enabled()
	if aerr != nil {
		autoOn = false
		fmt.Fprintf(os.Stderr, "读取开机自启状态失败: %v\n", aerr)
	}
	exePath, exeErr := os.Executable()

	actions := tray.Actions{
		OpenWeb: func() {
			addr := startWeb()
			if cfg.Web.OpensBrowser() {
				OpenBrowser("http://" + addr)
			}
		},
		CloseWeb:         stopWeb,
		AutostartEnabled: autoOn,
		SetAutostart: func(on bool) error {
			if exeErr != nil {
				return exeErr
			}
			if on {
				return autostart.Enable(exePath)
			}
			return autostart.Disable()
		},
		WebDAVEnabled:   davState().Enabled,
		SetWebDAV:       setDAV,
		AutoSyncEnabled: syncSwitch.On(),
		SetAutoSync: func(on bool) error {
			// 写回配置：打开用默认 15s，关闭写 -1
			secs := 0
			if !on {
				secs = -1
			}
			if werr := config.SetKey(cfgPath, "mount.sync_seconds", strconv.Itoa(secs)); werr != nil {
				return werr
			}
			syncSwitch.Set(on)
			return nil
		},
		Quit: func() {
			setState(i18n.T("tray.state.unmounted"))
			// 先停 WebDAV 再卸卷，避免客户端读到已关闭的索引
			_ = stopDAV()
			unmountAll()
			stopWeb()
			rootCancel()
		},
	}

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
			if entries, lerr := v.idx.List(rootCtx, "/"); lerr == nil {
				used, _ := v.idx.Used(rootCtx)
				lines = append(lines, fmt.Sprintf(i18n.T("tray.tip.vol"), v.name, len(entries), humanBytes(used)))
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
			tray.SetTooltip(tooltipText())
			select {
			case <-rootCtx.Done():
				return
			case <-ticker.C:
			}
		}
	})

	if tray.Supported() {
		tray.Run(actions, connText)
	} else {
		// 无图形环境：不起托盘，挂载与自动同步照常跑，Ctrl+C / SIGINT 退出
		fmt.Fprintln(os.Stderr, i18n.T("tray.headless"))
		sigCtx, stop := signal.NotifyContext(rootCtx, os.Interrupt)
		defer stop()
		<-sigCtx.Done()
	}

	// 托盘退出后收尾：停挂盘服务、卸载卷、取消后台任务
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
