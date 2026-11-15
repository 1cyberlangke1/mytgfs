package main

// mytgfs CLI 入口。
//
// 用法：
//   mytgfs auth bot                        bot 登录
//   mytgfs auth user                       手机号登录
//   mytgfs channel init --bot <username>   建私有频道并给 bot 提权
//   mytgfs sync [--limit N]                拉频道历史重建本地索引

import (
	"context"
	"flag"
	"fmt"
	"mytgfs/internal/app"
	"mytgfs/internal/authsvc"
	"mytgfs/internal/autostart"
	"mytgfs/internal/channel"
	"mytgfs/internal/config"
	"mytgfs/internal/crypto"
	"mytgfs/internal/datadir"
	"mytgfs/internal/fm"
	"mytgfs/internal/i18n"
	"mytgfs/internal/index"
	"mytgfs/internal/logging"
	"mytgfs/internal/push"
	"mytgfs/internal/sessionstore"
	tgsync "mytgfs/internal/sync"
	"mytgfs/internal/telegramstore"
	"mytgfs/internal/tgclient"
	"mytgfs/internal/trayapp"
	"mytgfs/internal/ui"
	"mytgfs/internal/uploadcache"
	"mytgfs/internal/volume"
	"mytgfs/internal/web"
	"net"
	"os"
	"os/signal"
	"strings"
	"sync"
	"time"
)

func runWeb(args []string) {
	fs := flag.NewFlagSet("web", flag.ExitOnError)
	port := fs.Int("port", 0, i18n.T("cli.flag.port_random"))
	_ = fs.Parse(args)
	portSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "port" {
			portSet = true
		}
	})

	const cfgPath = "config.toml"
	cfg, err := config.Load(cfgPath)

	// 端口优先级：命令行 --port > config.toml [web].port > 默认 8720
	portNum := cfg.Web.ListenPort()
	if portSet {
		portNum = *port
	}
	addr := fmt.Sprintf("127.0.0.1:%d", portNum)
	if portNum == 0 {
		ln, lerr := net.Listen("tcp", "127.0.0.1:0")
		if lerr != nil {
			fatal(lerr.Error())
		}
		addr = ln.Addr().String()
		_ = ln.Close()
	}
	url := "http://" + addr
	ui.Out(i18n.T("cli.web_url", url))

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	// 面板资源由前端构建产物内嵌，启停状态全部走 web.Options
	serve := func(opts web.Options) error {
		return web.ServeWith(ctx, addr, cfgPath, opts)
	}

	pushHub := push.NewHub(ctx)
	if e2eMockRequested() {
		// e2e：不连 Telegram，只起面板与假数据层（假数据由 -tags e2e 的包装器挂上）
		if err := serve(web.Options{Push: pushHub}); err != nil {
			fatal(err.Error())
		}
		return
	}
	if err != nil || cfg.APIHash() == "" {
		// 没配置也能开面板：只有配置编辑
		if err := serve(web.Options{Push: pushHub, Auth: authsvc.New(authsvc.Paths{ConfigPath: cfgPath}), Autostart: autostart.Control{}}); err != nil {
			fatal(err.Error())
		}
		return
	}
	key, err := crypto.LoadOrCreateKey("secrets/session.key")
	if err != nil {
		fatal(fmt.Sprintf("%s: %v", i18n.T("err.session_key_prefix"), err))
	}
	client := tgclient.New(cfg.Telegram.APIID, cfg.APIHash(),
		&sessionstore.Encrypted{Path: "secrets/bot.session", Key: key}, tgclient.Options{})

	err = client.Run(ctx, func(ctx context.Context) error {
		api, closePool, perr := client.PooledAPI(cfg.Mount.DownloadConnections())
		if perr != nil {
			return perr
		}
		defer closePool()
		appRoot := app.New(cfg, app.Options{Push: pushHub, ConfigPath: cfgPath})
		reg := app.NewVolumeRegistry[*webVolume]()
		vsync, vstop := context.WithCancel(ctx)
		var vwg sync.WaitGroup

		// 封面服务在装配层创建：挂载循环要在 pts 变化时失效它的缓存
		coverProvider := app.NewCoverProvider(appRoot, func(name string) (*app.VolumeFS, bool) {
			wv, ok := reg.Get(name)
			if !ok || wv.vf == nil {
				return nil, false
			}
			return wv.vf, true
		}, datadir.Path("thumbs"))

		// mountRuntime 打开一个卷并挂进运行时注册表，起该卷的自动同步协程。
		// 启动与面板建卷/attach 共用这一条路径，两端永远走同一份逻辑；
		// 同步参数挂载时现读：设置里改过的新卷立即用新值
		mountRuntime := func(name string, chatID int64) {
			vf, err := appRoot.OpenVolume(ctx, name, api)
			if err != nil {
				logging.Warn("tray.volume.open_failed", err, name)
				return
			}
			wv := &webVolume{vf: vf, mgr: vf.Files, idx: vf.Index, store: vf.Store}
			cur := appRoot.Config()
			// 别的端写入/删除后按频道差异自动跟上：面板读的是本地索引，不同步就一直是旧内容
			if cur.Mount.SyncOn() {
				syncInterval := trayapp.SyncInterval(cur.Mount.SyncSeconds)
				syncOn := trayapp.SyncEnabled(cfgPath, cur.Mount.SyncOn())
				differ := &tgsync.TelegramChannelDiffer{API: client.API(), ChannelID: chatID}
				vctx, vcancel := context.WithCancel(vsync)
				done := make(chan struct{})
				vwg.Go(func() {
					defer close(done)
					trayapp.AutoSyncWhile(vctx, trayapp.StatusNotifier(pushHub), wv.idx, differ, wv.store, syncInterval, nil, syncOn, func() {
						// pts 推进＝频道有更新：头像缓存失效重建，事件驱动不轮询
						coverProvider.InvalidateVolume(name)
					})
				})
				wv.stop = func() { vcancel(); <-done }
			}
			reg.Put(name, wv)
		}

		// reconcile 让运行时卷表与配置文件强制一致：
		// 每次都从磁盘现读（外部编辑/CLI 建卷也会被收编），再对齐注册表。
		// 建卷/attach/删卷等一切配置变更都以调用它收尾，用户永远不需要重启
		// reconcileMu 串行化对账：面板建卷/删卷与周期对账可能同时进来，
		// 并发挂载会把同一个卷挂两次（两份队列抢同一个索引库）
		var reconcileMu sync.Mutex
		reconcile := func() {
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
				mountRuntime(v.Name, v.ChatID)
			}
			for _, name := range unmount {
				wv, ok := reg.Get(name)
				if !ok {
					continue
				}
				if wv.stop != nil {
					wv.stop() // 先停自动同步，避免并发使用已关闭的索引
				}
				_ = wv.vf.Close() // 关队列与索引
				reg.Delete(name)
			}
			// 全部对齐完再推：面板一次刷新就能看到最终卷列表
			pushHub.Publish(push.TopicStatus)
		}
		// 启动即对齐：配置里启用的卷全部挂上
		reconcile()
		// 断点断片超过一天就清：Telegram 服务端临时区约一天过期，续不上的断点只会堆积
		uploadcache.Sweep(datadir.Path("uploads"), 24*time.Hour)
		// 定期从配置文件对账：CLI 建卷、手编 config.toml 的外部改动自动被收编
		go func() {
			interval := trayapp.SyncInterval(cfg.Mount.SyncSeconds)
			if interval <= 0 {
				interval = trayapp.SyncInterval(0)
			}
			for {
				select {
				case <-ctx.Done():
					return
				case <-time.Tick(interval):
					reconcile()
				}
			}
		}()
		// 先停自动同步再关索引：顺序反了会写已关闭的库
		defer func() {
			vstop()
			vwg.Wait()
			closeWebVolumes(reg.Snapshot())
		}()

		return serve(web.Options{
			Auth: authsvc.New(authsvc.Paths{ConfigPath: cfgPath}),
			// 开机自启与命令行共用同一套系统注册项
			Autostart: autostart.Control{},
			Files: func(name string) (fm.Files, bool) {
				v, ok := reg.Get(name)
				if !ok {
					return nil, false
				}
				return v.mgr, true
			},
			Volumes: func() []string {
				return reg.Names()
			},
			// 设置保存后立即应用到运行中的卷：并发数、读块、日志级别即时生效
			OnSettingsChanged: func(cur config.Config) {
				for _, wv := range reg.Snapshot() {
					wv.vf.Store.SetParallel(cur.Mount.DownloadConnections())
					wv.vf.Store.SetUploadParallel(cur.Mount.UploadParallelism())
					wv.vf.FS.SetChunk(cur.Mount.ReadChunk())
				}
				logging.SetLevel(cur.Log.Level)
				pushHub.Publish(push.TopicStatus)
			},
			// 建卷需要 user 会话：临时开一条，建完就关
			CreateVolume: func(ctx context.Context, name, title string) (any, error) {
				// 重名在建频道之前挡掉，避免建出孤儿频道
				if _, _, exists := appRoot.Volume(name); exists {
					return nil, config.ErrVolumeExists
				}
				bn := readBotUsername(ctx, cfg, cfg.APIHash(), key)
				if bn == "" {
					return nil, fmt.Errorf("%s", i18n.T("err.bot_not_ready"))
				}
				uc := tgclient.New(cfg.Telegram.APIID, cfg.APIHash(),
					&sessionstore.Encrypted{Path: "secrets/user.session", Key: key}, tgclient.Options{})
				var res volume.Result
				rerr := uc.Run(ctx, func(ctx context.Context) error {
					r, cerr := volume.Create(ctx, uc.API(), volume.Request{
						Name: name, Title: title, BotUsername: bn,
					})
					res = r
					if cerr != nil {
						return cerr
					}
					// 建完频道后才发现已取消：先把频道删掉，不能写进配置留半挂状态
					if ctx.Err() != nil {
						_ = volume.CleanupCancelled(ctx, uc.API(), res)
						return ctx.Err()
					}
					vol := config.Volume{
						Name:    res.Name,
						ChatID:  res.ChannelID,
						Enabled: true,
					}
					// 建卷唯一写入口：写盘与内存副本一步完成，随后立即挂载推送
					return appRoot.MutateConfig(func(p string) error {
						return config.AppendVolume(p, vol)
					})
				})
				if rerr != nil {
					return nil, rerr
				}
				if res.AvatarErr != nil {
					logging.Warn("log.cover_set_failed", res.AvatarErr, res.Name)
				}
				reconcile()
				return map[string]any{
					"name":       res.Name,
					"channel_id": res.ChannelID,
					"chat_id":    res.ChatID,
					"muted":      res.Muted,
				}, nil
			},
			// 删卷：删频道 → 移出配置 → 删索引
			DeleteVolume: func(ctx context.Context, name string) error {
				var target config.Volume
				found := false
				for _, v := range appRoot.Config().Volumes {
					if strings.EqualFold(v.Name, name) {
						target, found = v, true
						break
					}
				}
				if !found {
					return config.ErrVolumeNotFound
				}
				uc := tgclient.New(cfg.Telegram.APIID, cfg.APIHash(),
					&sessionstore.Encrypted{Path: "secrets/user.session", Key: key}, tgclient.Options{})
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
				// 删卷唯一写入口：写盘与内存副本一步完成
				if rerr := appRoot.MutateConfig(func(p string) error {
					return config.RemoveVolume(p, name)
				}); rerr != nil {
					return rerr
				}
				// 运行时账本对齐配置：停同步、关队列与索引、从注册表摘除，一次完成
				reconcile()
				// 缓存清理：缩略图按卷分目录整块删，封面按频道 id 前缀删
				web.PurgeVolumeCache(datadir.Path("thumbs"), name)
				coverProvider.PurgeChat(volume.NormalizeChatID(target.ChatID))
				for _, suffix := range []string{"", "-wal", "-shm"} {
					_ = os.Remove(datadir.Path("index-" + name + ".db" + suffix))
				}
				return nil
			},
			// 添加已有频道：用 bot 会话校验频道可见性与发言权
			AttachVolume: func(ctx context.Context, name string, chatID int64) (any, error) {
				if _, _, exists := appRoot.Volume(name); exists {
					return nil, config.ErrVolumeExists
				}
				norm := volume.NormalizeChatID(chatID)
				for _, v := range appRoot.Config().Volumes {
					if v.ChatID == norm {
						return nil, config.ErrChannelAttached
					}
				}
				res, aerr := volume.Attach(ctx, api, volume.AttachRequest{Name: name, ChatID: chatID})
				if aerr != nil {
					return nil, aerr
				}
				vol := config.Volume{Name: res.Name, ChatID: res.ChannelID, Enabled: true}
				// attach 唯一写入口：写盘与内存副本一步完成，随后立即挂载推送
				if werr := appRoot.MutateConfig(func(p string) error {
					return config.AppendVolume(p, vol)
				}); werr != nil {
					return nil, werr
				}
				reconcile()
				return map[string]any{
					"name":       res.Name,
					"channel_id": res.ChannelID,
					"chat_id":    res.ChatID,
					"title":      res.Title,
				}, nil
			},
			// 列频道：getDialogs 只有 user 会话能做，走装配根开临时连接
			Channels: func(ctx context.Context) ([]channel.Item, error) {
				return app.New(cfg, app.Options{ConfigPath: cfgPath}).Channels(ctx)
			},
			// 卷用量：面板卷标签直接显示文件数与占用
			Stats: func(name string) (int64, int64, bool) {
				v, ok := reg.Get(name)
				if !ok {
					return 0, 0, false
				}
				files, ferr := v.idx.CountFiles(ctx)
				if ferr != nil {
					return 0, 0, false
				}
				used, uerr := v.idx.Used(ctx)
				if uerr != nil {
					return 0, 0, false
				}
				return int64(files), used, true
			},
			Push: pushHub,
			// 分片上传的临时文件统一按时间清扫
			TempDir: datadir.Path("tmp"),
			// 下载目录：留空时由面板按 data/downloads 解析
			DownloadsDir: cfg.Downloads.EffectiveDir(),
			// 清空已结束的队列记录：面板按钮直接落到各卷的持久化表
			ClearQueue: func(ctx context.Context) (int64, error) {
				var total int64
				for _, wv := range reg.Snapshot() {
					n, cerr := wv.vf.ClearQueue(ctx)
					if cerr != nil {
						return total, cerr
					}
					total += n
				}
				return total, nil
			},
			// 封面就是频道头像：复用装配层的封面服务
			Cover: coverProvider,
			// 重建索引：用 user 会话全量拉频道历史后覆盖本地节点
			Rebuild: func(ctx context.Context, name string) (any, error) {
				wv, ok := reg.Get(name)
				if !ok {
					return nil, app.ErrVolumeNotFound{Name: name}
				}
				n, rerr := appRoot.RebuildIndex(ctx, wv.idx, volume.NormalizeChatID(wv.vf.Volume.ChatID))
				if rerr != nil {
					return nil, rerr
				}
				return map[string]any{"files": n}, nil
			},
			// 视频缩略图：Telegram 已生成的缩略图，按卷转发
			VideoThumb: func(ctx context.Context, vol, path string) ([]byte, string, error) {
				wv, ok := reg.Get(vol)
				if !ok {
					return nil, "", os.ErrNotExist
				}
				return wv.vf.VideoThumb(ctx, path)
			},
			// 面板自报状态：卷统计与上传队列都来自已打开的索引
			Status: func(ctx context.Context) (any, error) {
				return webStatus(ctx, reg.Snapshot()), nil
			},
		})
	})
	if err == nil {
		return
	}
	if tgclient.IsAuthLost(err) {
		ui.Errf("%s\n", i18n.T("err.auth_lost_hint"))
		if serr := serve(web.Options{Push: pushHub, Auth: authsvc.New(authsvc.Paths{ConfigPath: cfgPath}), Autostart: autostart.Control{}}); serr != nil {
			fatal(serr.Error())
		}
		return
	}
	fatal(err.Error())
}

// webVolume 是一个卷在面板里用到的后端。
type webVolume struct {
	vf    *app.VolumeFS
	mgr   *fm.Manager
	idx   *index.Index
	store *telegramstore.Store
	// stop 停掉该卷的自动同步协程并等它退出；删卷时先调它再关索引，
	// 避免同步协程并发使用已关闭的库。未启动同步的卷为 nil。
	stop func()
}

// closeWebVolumes 先停每个卷的队列，再关索引。

func closeWebVolumes(vols []*webVolume) {
	for _, v := range vols {
		if v.vf != nil {
			_ = v.vf.Close()
			continue
		}
		if v.idx != nil {
			_ = v.idx.Close()
		}
	}
}

// chatID 把 channel ID 转成 Bot API 风格的 -100... 形式。
