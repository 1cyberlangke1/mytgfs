package trayapp

// 托盘主管进程：挂载卷、自动增量同步、按需开关 Web 面板、托盘菜单。
//
// 这个包不依赖命令行参数解析，方便 CLI 的 `tray` 子命令和
// GUI 子系统的 `mytgfs-tray.exe` 共用同一套逻辑。

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"mytgfs/internal/autostart"
	"mytgfs/internal/config"
	"mytgfs/internal/crypto"
	"mytgfs/internal/datadir"
	"mytgfs/internal/i18n"
	"mytgfs/internal/index"
	"mytgfs/internal/sessionstore"
	tgsync "mytgfs/internal/sync"
	"mytgfs/internal/telegramstore"
	"mytgfs/internal/tgclient"
	"mytgfs/internal/tray"
	"mytgfs/internal/vfs"
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
// 输入：ctx；fsys 挂载文件系统；idx 索引；d 差异源；interval 间隔；
//
//	mu 与手动同步共用的互斥锁（可为 nil）
//
// 输出：无（ctx 取消时返回）
// 预期：启动即同步一次；单轮失败只记日志不退出；有变化才发刷新通知
func AutoSync(ctx context.Context, fsys *vfs.FS, idx *index.Index, d tgsync.ChannelDiffer, interval time.Duration, mu *sync.Mutex) {
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
		res, err := syncOnce()
		if ctx.Err() != nil {
			return
		}
		switch {
		case err != nil:
			fmt.Fprintf(os.Stderr, "自动同步失败: %v\n", err)
		case res.Pulled > 0 || res.Deleted > 0:
			fsys.NotifyPaths(res.Changed, res.Removed)
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
	cfgPath := opts.ConfigPath
	if cfgPath == "" {
		cfgPath = "config.toml"
	}
	port := opts.Port
	if port <= 0 {
		port = 8720
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("读取配置: %w", err)
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
		fsys   *vfs.FS
		differ tgsync.ChannelDiffer
		mu     sync.Mutex // 自动同步与手动同步互斥
	}
	var volMu sync.Mutex
	var volumes []*trayVolume

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
				return map[string]any{"state": st, "auth": auth, "volumes": list}, nil
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
					if res.Pulled > 0 || res.Deleted > 0 {
						v.fsys.NotifyPaths(res.Changed, res.Removed)
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
		store := telegramstore.New(client.API(), channelID)
		vctx, vcancel := context.WithCancel(rootCtx)
		fsys := vfs.New(vctx, idx, store, os.TempDir())
		differ := &tgsync.TelegramChannelDiffer{API: client.API(), ChannelID: channelID}
		tv := &trayVolume{name: name, idx: idx, fsys: fsys, differ: differ}

		volMu.Lock()
		volumes = append(volumes, tv)
		volMu.Unlock()

		var vwg sync.WaitGroup
		if interval := SyncInterval(cfg.Mount.SyncSeconds); interval > 0 {
			vwg.Add(1)
			go func() {
				defer vwg.Done()
				AutoSync(vctx, fsys, idx, differ, interval, &tv.mu)
			}()
		}

		mu.Lock()
		cancels = append(cancels, vcancel)
		mu.Unlock()

		wg.Add(1)
		go func() {
			defer wg.Done()
			if merr := vfs.Mount(vctx, fsys, mp, name); merr != nil {
				fmt.Fprintf(os.Stderr, "%s\n", i18n.T("tray.mount.failed", name, merr))
			}
			vcancel()
			vwg.Wait()
			_ = idx.Close()
			volMu.Lock()
			for i, v := range volumes {
				if v == tv {
					volumes = append(volumes[:i], volumes[i+1:]...)
					break
				}
			}
			volMu.Unlock()
		}()
	}

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

	// 连接循环：断网由 gotd 内部无限重连；Run 只在永久错误或 ctx 取消时返回
	wg.Add(1)
	go func() {
		defer wg.Done()
		rerr := client.Run(rootCtx, func(ctx context.Context) error {
			// 连接就绪后读一次 bot 用户名，建卷时用它拉 bot 进新频道
			if self, serr := client.Self(ctx); serr == nil {
				setBotUsername(self.Username)
			}
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
	}()

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
			OpenBrowser("http://" + addr)
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
		Quit: func() {
			setState(i18n.T("tray.state.unmounted"))
			unmountAll()
			stopWeb()
			rootCancel()
		},
	}
	if tray.Supported() {
		tray.Run(actions, connText)
	} else {
		// 无图形环境：不起托盘，挂载与自动同步照常跑，Ctrl+C / SIGINT 退出
		fmt.Fprintln(os.Stderr, i18n.T("tray.headless"))
		sigCtx, stop := signal.NotifyContext(rootCtx, os.Interrupt)
		defer stop()
		<-sigCtx.Done()
	}

	// 托盘退出后收尾：再确保卸载与取消，最多等 5 秒让 WinFsp 完成卸载
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
