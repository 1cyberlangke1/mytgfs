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
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"

	"mytgfs/internal/app"
	"mytgfs/internal/auth"
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
	"mytgfs/internal/version"
	"mytgfs/internal/vfs"
	"mytgfs/internal/volume"
	"mytgfs/internal/web"
)

func main() {
	logging.SetupFromConfig("config.toml")
	os.Args = append([]string{os.Args[0]}, stripJSONFlag(os.Args[1:])...)
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "version":
		// 版本信息：给排障与脚本用，支持 --json
		ui.Print(version.Get())
		ui.Printf("%s\n", version.String())
	case "auth":
		if len(os.Args) < 3 {
			usage()
			os.Exit(2)
		}
		runAuth(os.Args[2])
	case "channel":
		if len(os.Args) < 3 {
			usage()
			os.Exit(2)
		}
		switch os.Args[2] {
		case "create":
			runChannelCreate(os.Args[3:])
		case "init":
			runChannelInit(os.Args[3:])
		case "list":
			runChannelList(os.Args[3:])
		case "add":
			runChannelAdd(os.Args[3:])
		default:
			usage()
			os.Exit(2)
		}
	case "sync":
		runSync(os.Args[2:])
	case "check":
		runCheck(os.Args[2:])
	case "cp":
		runCopy(os.Args[2:])
	case "put":
		runPut(os.Args[2:])
	case "ls":
		runLs(os.Args[2:])
	case "mkdir":
		runMkdir(os.Args[2:])
	case "rm":
		runRm(os.Args[2:])
	case "mv":
		runMv(os.Args[2:])
	case "get":
		runGet(os.Args[2:])
	case "search":
		runSearch(os.Args[2:])
	case "config":
		runConfig(os.Args[2:])
	case "volume":
		runVolume(os.Args[2:])
	case "web":
		runWeb(os.Args[2:])
	case "startup":
		runStartup(os.Args[2:])
	case "tray":
		runTray(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
}

// runAuth 处理 bot / user 登录。
func runAuth(sub string) {
	apiID, apiHash := loadAPI()
	key, err := crypto.LoadOrCreateKey("secrets/session.key")
	if err != nil {
		fatal(fmt.Sprintf("%s: %v", i18n.T("err.session_key_prefix"), err))
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	switch sub {
	case "bot":
		token := readTrim("secrets/bot_token")
		if token == "" {
			fmt.Fprint(os.Stderr, i18n.T("prompt.bot_token"))
			token = readLine()
		}
		p := auth.Params{
			APIID:   apiID,
			APIHash: apiHash,
			Session: &sessionstore.Encrypted{Path: "secrets/bot.session", Key: key},
		}
		if err := auth.LoginBot(ctx, p, token); err != nil {
			fatal(err.Error())
		}
	case "user":
		phone := os.Getenv("MYTGFS_PHONE")
		if phone == "" {
			fmt.Fprint(os.Stderr, i18n.T("prompt.phone"))
			phone = readLine()
		}
		password := os.Getenv("MYTGFS_2FA")
		p := auth.Params{
			APIID:   apiID,
			APIHash: apiHash,
			Session: &sessionstore.Encrypted{Path: "secrets/user.session", Key: key},
		}
		if err := auth.LoginUser(ctx, p, phone, password); err != nil {
			fatal(err.Error())
		}
	default:
		usage()
		os.Exit(2)
	}
}

// runChannelCreate 创建频道，可选把 bot 设为管理员。
func runChannelCreate(args []string) {
	fs := flag.NewFlagSet("channel create", flag.ExitOnError)
	name := fs.String("name", "mytgfs", i18n.T("cli.flag.channel_title"))
	bot := fs.String("bot", "", i18n.T("cli.flag.bot_optional"))
	_ = fs.Parse(args)

	apiID, apiHash := loadAPI()
	key, err := crypto.LoadOrCreateKey("secrets/session.key")
	if err != nil {
		fatal(fmt.Sprintf("%s: %v", i18n.T("err.session_key_prefix"), err))
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	client := tgclient.New(apiID, apiHash,
		&sessionstore.Encrypted{Path: "secrets/user.session", Key: key},
		tgclient.Options{})

	var res *channel.Result
	err = client.Run(ctx, func(ctx context.Context) error {
		r, err := channel.Create(ctx, client.API(), *name)
		if err != nil {
			return err
		}
		if *bot != "" {
			id, err := channel.AddBot(ctx, client.API(), r, *bot)
			if err != nil {
				return err
			}
			r.BotID = id
		}
		res = r
		return nil
	})
	if err != nil {
		failErr(i18n.T("err.channel_init_prefix"), err)
	}
	ui.Out(i18n.T("channel.ok", res.ChannelID, res.AccessHash, chatID(res.ChannelID)))
}

// runChannelList 列出当前 user 能管理的频道。
func runChannelList(args []string) {
	fs := flag.NewFlagSet("channel list", flag.ExitOnError)
	_ = fs.Parse(args)

	apiID, apiHash := loadAPI()
	key, err := crypto.LoadOrCreateKey("secrets/session.key")
	if err != nil {
		fatal(fmt.Sprintf("%s: %v", i18n.T("err.session_key_prefix"), err))
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	client := tgclient.New(apiID, apiHash,
		&sessionstore.Encrypted{Path: "secrets/user.session", Key: key},
		tgclient.Options{})

	var out []channel.Item
	err = client.Run(ctx, func(ctx context.Context) error {
		items, err := channel.List(ctx, client.API())
		if err != nil {
			return err
		}
		out = items
		for _, it := range items {
			ui.Printf("%d\t%s\t%s\tcreator=%v\tadmin=%v\n", it.ID, it.Title, it.Username, it.Creator, it.Admin)
		}
		return nil
	})
	if err != nil {
		failErr(i18n.T("err.list_channels"), err)
	}
	ui.Print(map[string]any{"channels": out})
}

// runChannelAdd 把 bot 加到已有频道。
func runChannelAdd(args []string) {
	fs := flag.NewFlagSet("channel add", flag.ExitOnError)
	channelID := fs.Int64("channel", 0, "channel id")
	bot := fs.String("bot", "", i18n.T("cli.flag.bot"))
	_ = fs.Parse(args)

	if *channelID == 0 || *bot == "" {
		fatal(i18n.T("err.need_channel_bot"))
	}
	apiID, apiHash := loadAPI()
	key, err := crypto.LoadOrCreateKey("secrets/session.key")
	if err != nil {
		fatal(fmt.Sprintf("%s: %v", i18n.T("err.session_key_prefix"), err))
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	client := tgclient.New(apiID, apiHash,
		&sessionstore.Encrypted{Path: "secrets/user.session", Key: key},
		tgclient.Options{})

	var res *channel.Result
	err = client.Run(ctx, func(ctx context.Context) error {
		r, err := channel.Add(ctx, client.API(), *channelID, *bot)
		res = r
		return err
	})
	if err != nil {
		failErr(i18n.T("err.add_bot"), err)
	}
	ui.Out(i18n.T("channel.ok", res.ChannelID, res.AccessHash, chatID(res.ChannelID)))
}

// runChannelInit 建频道并给 bot 提权（等价 channel create --bot）。
func runChannelInit(args []string) {
	fs := flag.NewFlagSet("channel init", flag.ExitOnError)
	name := fs.String("name", "mytgfs", i18n.T("cli.flag.channel_title"))
	botUsername := fs.String("bot", "", i18n.T("cli.flag.bot_no_at"))
	_ = fs.Parse(args)

	if *botUsername == "" {
		fatal(i18n.T("err.bot_required"))
	}
	apiID, apiHash := loadAPI()
	key, err := crypto.LoadOrCreateKey("secrets/session.key")
	if err != nil {
		fatal(fmt.Sprintf("%s: %v", i18n.T("err.session_key_prefix"), err))
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	client := tgclient.New(apiID, apiHash,
		&sessionstore.Encrypted{Path: "secrets/user.session", Key: key},
		tgclient.Options{})

	var res *channel.Result
	err = client.Run(ctx, func(ctx context.Context) error {
		r, err := channel.Init(ctx, client.API(), *botUsername, *name)
		res = r
		return err
	})
	if err != nil {
		failErr(i18n.T("err.channel_init_prefix"), err)
	}
	ui.Out(i18n.T("channel.ok", res.ChannelID, res.AccessHash, chatID(res.ChannelID)))
}

// resolveVolumeName 决定命令作用于哪个卷。
//
// 输入：cfg 配置；name 命令行 --name（可为空）
// 输出：卷名
// 预期：显式 --name 原样返回；留空时取第一个卷，没有卷配置时用默认名 mytgfs
func resolveVolumeName(cfg config.Config, name string) string {
	if name != "" {
		return name
	}
	if len(cfg.Volumes) > 0 {
		return cfg.Volumes[0].Name
	}
	return "mytgfs"
}

// runSync 增量拉取频道消息并更新本地索引。
// runSync 用频道 pts 游标增量同步，或用 user 会话回填历史。
//
// 输入：命令行参数（--limit 单轮上限、--name 卷名、--backfill 走历史回填）
// 输出：无（打印统计；失败退出）
// 预期：默认走 updates.getChannelDifference，bot 会话即可，只拉新变化；
//
//	--backfill 走 messages.getHistory（user-only），用于首次深挖旧文件
func runSync(args []string) {
	fs := flag.NewFlagSet("sync", flag.ExitOnError)
	limit := fs.Int("limit", 100, i18n.T("cli.flag.limit_sync"))
	name := fs.String("name", "", i18n.T("cli.flag.volume"))
	rebuild := fs.Bool("rebuild", false, i18n.T("cli.flag.rebuild"))
	_ = fs.Parse(args)

	a, err := app.Load(app.Options{})
	if err != nil {
		fatal(fmt.Sprintf("%s: %v", i18n.T("err.config_read"), err))
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	entries := 0
	err = a.Run(ctx, *name, func(ctx context.Context, s *app.Session) error {
		if *rebuild {
			nodes, rerr := a.AllNodes(ctx, s.Volume.ChatID)
			if rerr != nil {
				return rerr
			}
			n, rerr := tgsync.Rebuild(ctx, s.Index, nodes)
			if rerr != nil {
				return rerr
			}
			ui.Out(i18n.T("cli.rebuild_stats", n))
		} else {
			d := &tgsync.TelegramChannelDiffer{API: s.Client.API(), ChannelID: s.Volume.ChatID}
			res, serr := tgsync.SyncChannel(ctx, s.Index, d, s.Store(), *limit)
			if serr != nil {
				return serr
			}
			if res.Truncated {
				ui.Out(i18n.T("warn.diff_too_long"))
			}
			ui.Out(i18n.T("cli.sync_stats", res.Pulled, res.Deleted, res.Pts, res.Rounds))
		}
		children, cerr := s.Index.Children(ctx, 0)
		if cerr != nil {
			return cerr
		}
		entries = len(children)
		return nil
	})
	if err != nil {
		failErr(i18n.T("err.sync"), err)
	}
	ui.Out(i18n.T("cli.sync_ok", entries))
}

// runWeb 启动本地面板。
//
// 输入：--port 端口（不传则用 config.toml [web].port，传 0 用随机端口）
// 输出：无（阻塞到 Ctrl+C）
// 预期：
//   - 有可用 bot 凭证时，每个启用的卷都能在面板里浏览/上传/移动/删除
//   - 凭证失效也不影响面板启动，只是没有文件管理
//   - 只打印地址，不改动用户环境里的浏览器
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
		appRoot := app.New(cfg, app.Options{Push: pushHub})
		vols := openWebVolumes(ctx, appRoot, api)
		defer closeWebVolumes(vols)

		// 别的端写入/删除后按频道差异自动跟上：面板读的是本地索引，不同步就一直是旧内容
		vsync, vstop := context.WithCancel(ctx)
		var vwg sync.WaitGroup
		if interval := trayapp.SyncInterval(cfg.Mount.SyncSeconds); cfg.Mount.SyncOn() {
			syncOn := trayapp.SyncEnabled(cfgPath, cfg.Mount.SyncOn())
			for _, vol := range cfg.Volumes {
				if !vol.Enabled {
					continue
				}
				wv, ok := vols[vol.Name]
				if !ok {
					continue
				}
				differ := &tgsync.TelegramChannelDiffer{API: client.API(), ChannelID: vol.ChatID}
				vwg.Go(func() {
					trayapp.AutoSyncWhile(vsync, trayapp.StatusNotifier(pushHub), wv.idx, differ, wv.store, interval, nil, syncOn)
				})
			}
		}
		// 先停自动同步再关索引：顺序反了会写已关闭的库
		defer func() {
			vstop()
			vwg.Wait()
		}()

		return serve(web.Options{
			Auth: authsvc.New(authsvc.Paths{ConfigPath: cfgPath}),
			// 开机自启与命令行共用同一套系统注册项
			Autostart: autostart.Control{},
			Files: func(name string) (fm.Files, bool) {
				v, ok := vols[name]
				if !ok {
					return nil, false
				}
				return v.mgr, true
			},
			Volumes: func() []string {
				names := make([]string, 0, len(vols))
				for _, v := range cfg.Volumes {
					if _, ok := vols[v.Name]; ok {
						names = append(names, v.Name)
					}
				}
				return names
			},
			// 建卷需要 user 会话：临时开一条，建完就关
			CreateVolume: func(ctx context.Context, name, title string) (any, error) {
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
					return cerr
				})
				if rerr != nil {
					return nil, rerr
				}
				if res.AvatarErr != nil {
					logging.Warn("log.cover_set_failed", res.AvatarErr, res.Name)
				}
				vol := config.Volume{
					Name:    res.Name,
					ChatID:  res.ChannelID,
					Enabled: true,
				}
				if aerr := config.AppendVolume(cfgPath, vol); aerr != nil {
					return nil, aerr
				}
				return map[string]any{
					"name":       res.Name,
					"channel_id": res.ChannelID,
					"chat_id":    res.ChatID,
					"muted":      res.Muted,
				}, nil
			},
			// 删卷：删频道 → 移出配置 → 删索引
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
				if rerr := config.RemoveVolume(cfgPath, name); rerr != nil {
					return rerr
				}
				for _, suffix := range []string{"", "-wal", "-shm"} {
					_ = os.Remove(datadir.Path("index-" + name + ".db" + suffix))
				}
				return nil
			},
			// 添加已有频道：用 bot 会话校验频道可见性与发言权
			AttachVolume: func(ctx context.Context, name string, chatID int64) (any, error) {
				res, aerr := volume.Attach(ctx, api, volume.AttachRequest{Name: name, ChatID: chatID})
				if aerr != nil {
					return nil, aerr
				}
				vol := config.Volume{Name: res.Name, ChatID: res.ChannelID, Enabled: true}
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
			// 列频道：getDialogs 只有 user 会话能做，走装配根开临时连接
			Channels: func(ctx context.Context) ([]channel.Item, error) {
				return app.New(cfg, app.Options{ConfigPath: cfgPath}).Channels(ctx)
			},
			// 卷用量：面板卷标签直接显示文件数与占用
			Stats: func(name string) (int64, int64, bool) {
				v, ok := vols[name]
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
				for _, wv := range vols {
					n, cerr := wv.vf.ClearQueue(ctx)
					if cerr != nil {
						return total, cerr
					}
					total += n
				}
				return total, nil
			},
			// 封面就是频道头像：按卷名找到对应 Store
			Cover: app.NewCoverProvider(appRoot, func(name string) (*app.VolumeFS, bool) {
				wv, ok := vols[name]
				if !ok {
					return nil, false
				}
				return wv.vf, true
			}),
			// 面板自报状态：卷统计与上传队列都来自已打开的索引
			Status: func(ctx context.Context) (any, error) {
				return webStatus(ctx, cfg, vols), nil
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
	davfs *vfs.FileSystem
	idx   *index.Index
	store *telegramstore.Store
}

// openWebVolumes 为每个启用的卷打开文件系统后端。
//
// 输入：ctx；a 装配根；api 已认证的 MTProto 客户端
// 输出：卷名 → 后端；打开失败的卷直接跳过（面板里就不显示）
// 预期：只读索引 + 可写存储，每个卷独立 db 文件
func openWebVolumes(ctx context.Context, a *app.App, api telegramstore.API) map[string]*webVolume {
	out := map[string]*webVolume{}
	for _, v := range a.Config().Volumes {
		if !v.Enabled {
			continue
		}
		vf, err := a.OpenVolume(ctx, v.Name, api)
		if err != nil {
			logging.Warn("tray.volume.open_failed", err, v.Name)
			continue
		}
		out[v.Name] = &webVolume{vf: vf, mgr: vf.Files, davfs: vf.FS, idx: vf.Index, store: vf.Store}
	}
	// 卷已就绪：推一条 status，面板连上后立即拿到真实卷列表，无需定时重试
	if len(out) > 0 {
		a.Events().Publish(push.TopicStatus)
	}
	return out
}

// closeWebVolumes 先停每个卷的队列，再关索引。
func closeWebVolumes(vols map[string]*webVolume) {
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
func chatID(channelID int64) int64 {
	return -1000000000000 - channelID
}

// loadAPI 读取 Telegram App 凭据。
//
// 输入：无（读 MYTGFS_API_ID 环境变量与 config.toml）
// 输出：api_id、api_hash
// 预期：api_id 先看环境变量，再看 config.toml 的 [telegram].api_id；
// 两者都没有时直接报错退出，不内置任何默认值（api_id 是应用所有者的身份）。
func loadAPI() (int, string) {
	apiID := 0
	if v := strings.TrimSpace(os.Getenv("MYTGFS_API_ID")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			fatal(fmt.Sprintf("%s: %q", i18n.T("err.bad_api_id"), v))
		}
		apiID = n
	}
	if apiID == 0 {
		if cfg, err := config.Load("config.toml"); err == nil {
			apiID = cfg.Telegram.APIID
		}
	}
	if apiID == 0 {
		fatal(i18n.T("err.missing_api_id"))
	}
	apiHash := readTrim("secrets/api_hash")
	if apiHash == "" {
		fatal(i18n.T("err.missing_api_hash"))
	}
	return apiID, apiHash
}

func usage() {
	if ui.JSONMode() {
		ui.Error("usage", fmt.Errorf("%s", i18n.T("cli.usage_short")))
		os.Exit(2)
	}
	ui.Errf("%s\n", i18n.T("cli.usage"))
}

func readTrim(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func readLine() string {
	var s string
	fmt.Fscanln(os.Stdin, &s)
	return strings.TrimSpace(s)
}

// stripJSONFlag 取出全局 --json 开关并返回其余参数。
//
// 输入：os.Args[1:]
// 输出：去掉所有 --json 的参数
// 预期：开关位置无关；出现在参数值里不会被误删（只匹配独立的 --json）
func stripJSONFlag(args []string) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		if a == "--json" {
			ui.EnableJSON()
			continue
		}
		out = append(out, a)
	}
	return out
}

func fatal(msg string) {
	if ui.JSONMode() {
		ui.Error("fatal", fmt.Errorf("%s", msg))
		os.Exit(1)
	}
	ui.Errf("FAIL: %s\n", msg)
	os.Exit(1)
}

// failErr 打印错误；授权失效时补一句重新登录提示。
//
// 输入：prefix 错误前缀；err 原始错误
// 输出：无（进程退出 1）
// 预期：会话失效不可自愈，提示重新 auth，避免用户反复重试同一命令
func failErr(prefix string, err error) {
	code := "error"
	switch {
	case tgclient.IsAuthLost(err):
		code = "auth_lost"
	case errors.Is(err, os.ErrNotExist):
		code = "not_found"
	case errors.Is(err, context.Canceled):
		code = "canceled"
	}
	if ui.JSONMode() {
		ui.Error(code, fmt.Errorf("%s: %w", prefix, err))
		os.Exit(1)
	}
	if code == "auth_lost" {
		fatal(fmt.Sprintf("%s: %v\n%s", prefix, err, i18n.T("err.auth_lost_hint")))
	}
	fatal(fmt.Sprintf("%s: %v", prefix, err))
}
