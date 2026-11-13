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
	"time"

	"mytgfs/internal/auth"
	"mytgfs/internal/channel"
	"mytgfs/internal/config"
	"mytgfs/internal/crypto"
	"mytgfs/internal/daemon"
	"mytgfs/internal/datadir"
	"mytgfs/internal/dav"
	"mytgfs/internal/eventbus"
	"mytgfs/internal/fm"
	"mytgfs/internal/i18n"
	"mytgfs/internal/index"
	"mytgfs/internal/ipc"
	"mytgfs/internal/logging"
	"mytgfs/internal/queue"
	"mytgfs/internal/sessionstore"
	tgsync "mytgfs/internal/sync"
	"mytgfs/internal/telegramstore"
	"mytgfs/internal/tgclient"
	"mytgfs/internal/trayapp"
	"mytgfs/internal/ui"
	"mytgfs/internal/uploadcache"
	"mytgfs/internal/version"
	"mytgfs/internal/web"
)

func main() {
	initLogging()
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
	case "cp":
		runCopy(os.Args[2:])
	case "put":
		runPut(os.Args[2:])
	case "volume":
		runVolume(os.Args[2:])
	case "mount":
		runMount(os.Args[2:])
	case "webdav":
		runWebDAV(os.Args[2:])
	case "unmap":
		runUnmap(os.Args[2:])
	case "umount":
		runUmount(os.Args[2:])
	case "daemon":
		runDaemon(os.Args[2:])
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

// initLogging 按配置初始化 slog 与轮转日志。
func initLogging() {
	cfg, err := config.Load("config.toml")
	if err != nil {
		cfg = config.Config{}
	}
	_, _ = logging.Setup(logging.Config{
		Level:      cfg.Log.Level,
		File:       cfg.Log.File,
		MaxSizeMB:  cfg.Log.MaxSizeMB,
		MaxBackups: cfg.Log.MaxBackups,
		MaxAgeDays: cfg.Log.MaxAgeDays,
	})
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
	name := fs.String("name", "mytgfs", "channel title")
	bot := fs.String("bot", "", "optional bot username")
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
	fmt.Println(i18n.T("channel.ok", res.ChannelID, res.AccessHash, chatID(res.ChannelID)))
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
		failErr("列出频道失败", err)
	}
	ui.Print(map[string]any{"channels": out})
}

// runChannelAdd 把 bot 加到已有频道。
func runChannelAdd(args []string) {
	fs := flag.NewFlagSet("channel add", flag.ExitOnError)
	channelID := fs.Int64("channel", 0, "channel id")
	bot := fs.String("bot", "", "bot username")
	_ = fs.Parse(args)

	if *channelID == 0 || *bot == "" {
		fatal("需要 --channel 和 --bot")
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
		failErr("添加 bot 失败", err)
	}
	fmt.Println(i18n.T("channel.ok", res.ChannelID, res.AccessHash, chatID(res.ChannelID)))
}

// runChannelInit 建频道并给 bot 提权（等价 channel create --bot）。
func runChannelInit(args []string) {
	fs := flag.NewFlagSet("channel init", flag.ExitOnError)
	name := fs.String("name", "mytgfs", "channel title")
	botUsername := fs.String("bot", "", "bot username without @")
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
	fmt.Println(i18n.T("channel.ok", res.ChannelID, res.AccessHash, chatID(res.ChannelID)))
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
	limit := fs.Int("limit", 100, "每轮事件上限 / 回填条数")
	name := fs.String("name", "mytgfs", "volume name")
	backfill := fs.Bool("backfill", false, "用 user 会话回填历史消息")
	_ = fs.Parse(args)

	cfg, err := config.Load("config.toml")
	if err != nil {
		fatal(fmt.Sprintf("读取配置: %v", err))
	}
	apiHash := cfg.APIHash()
	if apiHash == "" {
		fatal(i18n.T("err.missing_api_hash"))
	}
	key, err := crypto.LoadOrCreateKey("secrets/session.key")
	if err != nil {
		fatal(fmt.Sprintf("%s: %v", i18n.T("err.session_key_prefix"), err))
	}
	idx, err := index.Open(datadir.Path("index-" + *name + ".db"))
	if err != nil {
		fatal(fmt.Sprintf("打开索引: %v", err))
	}
	defer idx.Close()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	if *backfill {
		client := tgclient.New(cfg.Telegram.APIID, apiHash,
			&sessionstore.Encrypted{Path: "secrets/user.session", Key: key},
			tgclient.Options{})
		err = client.Run(ctx, func(ctx context.Context) error {
			f := tgsync.TelegramFetcher{
				API:        client.API(),
				ChannelID:  cfg.Telegram.ChannelID,
				AccessHash: cfg.Telegram.AccessHash,
			}
			return tgsync.Sync(ctx, idx, f, *limit)
		})
		if err != nil {
			failErr("回填失败", err)
		}
	} else {
		client := tgclient.New(cfg.Telegram.APIID, apiHash,
			&sessionstore.Encrypted{Path: "secrets/bot.session", Key: key},
			tgclient.Options{})
		var res tgsync.ChannelSyncResult
		err = client.Run(ctx, func(ctx context.Context) error {
			d := &tgsync.TelegramChannelDiffer{API: client.API(), ChannelID: cfg.Telegram.ChannelID}
			var serr error
			res, serr = tgsync.SyncChannel(ctx, idx, d, *limit)
			return serr
		})
		if err != nil {
			failErr("同步失败", err)
		}
		if res.Truncated {
			fmt.Println("警告: 差异过大，只拿到最新消息；可用 --backfill 补历史")
		}
		fmt.Printf("PULLED=%d DELETED=%d PTS=%d ROUNDS=%d\n", res.Pulled, res.Deleted, res.Pts, res.Rounds)
	}

	entries, err := idx.List(ctx, "/")
	if err != nil {
		fatal(fmt.Sprintf("读取索引: %v", err))
	}
	fmt.Printf("OK synced, root entries=%d\n", len(entries))
}

// runMount 已停用：FUSE/WinFsp 挂载层已移除，改用 WebDAV 挂载。
//
// 输入：任意参数（忽略）
// 输出：无；打印提示后以非零码退出
// 预期：不连接 Telegram、不写挂载状态，避免旧脚本误判挂载成功
func runMount(_ []string) {
	fatal(i18n.T("err.mount_removed"))
}

// runDaemon 管理后台守护进程。
func runDaemon(args []string) {
	if len(args) == 0 {
		usage()
		os.Exit(2)
	}
	switch args[0] {
	case "run":
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
		defer cancel()
		if err := daemon.Run(ctx); err != nil {
			fatal(err.Error())
		}
	case "start":
		exe, err := os.Executable()
		if err != nil {
			fatal(err.Error())
		}
		if err := daemon.Start(exe); err != nil {
			fatal(err.Error())
		}
		for i := 0; i < 30; i++ {
			if st, err := daemon.ReadState(); err == nil {
				fmt.Printf("OK daemon started pid=%d addr=%s\n", st.PID, st.Addr)
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		fatal("daemon 启动超时，检查 daemon.log")
	case "status":
		st, err := daemon.ReadState()
		if err != nil {
			fatal("daemon 未运行")
		}
		data, err := ipc.Call(context.Background(), st.Addr, st.Token, "status", nil)
		if err != nil {
			fatal(err.Error())
		}
		fmt.Println(string(data))
	case "stop":
		st, err := daemon.ReadState()
		if err != nil {
			fatal("daemon 未运行")
		}
		if _, err := ipc.Call(context.Background(), st.Addr, st.Token, "stop", nil); err != nil {
			fatal(err.Error())
		}
		fmt.Println("OK stopped")
	default:
		usage()
		os.Exit(2)
	}
}

// runWeb 启动临时本地 Web 面板。
// runWeb 启动本地面板。
//
// 输入：--port 端口（0 = 随机）、--no-open 不自动开浏览器
// 输出：无（阻塞到 Ctrl+C）
// 预期：
//   - 有可用 bot 凭证时，每个启用的卷都能在面板里浏览/上传/移动/删除
//   - 凭证失效也不影响面板启动，只是没有文件管理
func runWeb(args []string) {
	fs := flag.NewFlagSet("web", flag.ExitOnError)
	port := fs.Int("port", 0, "port (0 = random)")
	noOpen := fs.Bool("no-open", false, "do not open browser")
	_ = fs.Parse(args)

	addr := fmt.Sprintf("127.0.0.1:%d", *port)
	if *port == 0 {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			fatal(err.Error())
		}
		addr = ln.Addr().String()
		_ = ln.Close()
	}
	url := "http://" + addr
	fmt.Println("web:", url)
	if !*noOpen {
		trayapp.OpenBrowser(url)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	const cfgPath = "config.toml"
	serve := func(opts web.Options) error { return web.ServeWith(ctx, addr, cfgPath, opts) }

	cfg, err := config.Load(cfgPath)
	bus := eventbus.New()
	if err != nil || cfg.APIHash() == "" {
		// 没配置也能开面板：只有配置编辑
		if err := serve(web.Options{Bus: bus}); err != nil {
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
		vols := openWebVolumes(ctx, cfg, api)
		defer closeWebVolumes(vols)
		davSvc, mount := newMountControl(cfgPath, vols)
		defer davSvc.Stop()

		// 别的端写入/删除后按频道差异自动跟上：面板读的是本地索引，不同步就一直是旧内容
		vsync, vstop := context.WithCancel(ctx)
		var vwg sync.WaitGroup
		if interval := trayapp.SyncInterval(cfg.Mount.SyncSeconds); interval > 0 {
			for _, vol := range cfg.Volumes {
				if !vol.Enabled {
					continue
				}
				wv, ok := vols[vol.Name]
				if !ok {
					continue
				}
				differ := &tgsync.TelegramChannelDiffer{API: client.API(), ChannelID: vol.ChatID}
				vwg.Add(1)
				go func() {
					defer vwg.Done()
					trayapp.AutoSync(vsync, nil, wv.idx, differ, interval, nil)
				}()
			}
		}
		// 先停自动同步再关索引：顺序反了会写已关闭的库
		defer func() {
			vstop()
			vwg.Wait()
		}()

		return serve(web.Options{
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
			Mount: mount,
			Bus:   bus,
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
		fmt.Fprintln(os.Stderr, i18n.T("err.auth_lost_hint"))
		if serr := serve(web.Options{Bus: bus}); serr != nil {
			fatal(serr.Error())
		}
		return
	}
	fatal(err.Error())
}

// webVolume 是一个卷在面板里用到的后端。
type webVolume struct {
	mgr   *fm.Manager
	davfs *dav.FileSystem
	idx   *index.Index
}

// openWebVolumes 为每个启用的卷打开索引与存储。
//
// 输入：ctx；cfg 配置；api 已认证的 MTProto 客户端
// 输出：卷名 → 后端；打开失败的卷直接跳过（面板里就不显示）
// 预期：只读索引 + 可写存储，每个卷独立 db 文件
func openWebVolumes(ctx context.Context, cfg config.Config, api telegramstore.API) map[string]*webVolume {
	out := map[string]*webVolume{}
	for _, v := range cfg.Volumes {
		if !v.Enabled {
			continue
		}
		idx, err := index.Open(datadir.Path("index-" + v.Name + ".db"))
		if err != nil {
			fmt.Fprintln(os.Stderr, i18n.T("tray.mount.failed", v.Name, err))
			continue
		}
		// 索引是缓存：空了就用 user 会话按频道历史重建，避免面板一片空白
		if rerr := trayapp.RebuildIndexIfEmpty(ctx, cfg, idx, v.ChatID); rerr != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", i18n.T("tray.mount.failed", v.Name), rerr)
		}
		store := telegramstore.New(api, v.ChatID)
		store.SetParallel(cfg.Mount.DownloadConnections())
		store.SetUploadParallel(cfg.Mount.UploadParallelism())
		store.SetResumeStore(uploadcache.New(datadir.Path("uploads")))
		davfs := dav.New(ctx, idx, store, cfg.Mount.ReadChunk())
		// 面板上传同样走串行队列：批量拖拽不会互相抢 Telegram 配额
		davfs.SetQueue(queue.New(1, 3))
		out[v.Name] = &webVolume{
			mgr:   fm.New(davfs),
			davfs: davfs,
			idx:   idx,
		}
	}
	return out
}

// closeWebVolumes 关闭所有索引。
func closeWebVolumes(vols map[string]*webVolume) {
	for _, v := range vols {
		if v.idx != nil {
			_ = v.idx.Close()
		}
	}
}

// chatID 把 channel ID 转成 Bot API 风格的 -100... 形式。
func chatID(channelID int64) int64 {
	return -1000000000000 - channelID
}

func loadAPI() (int, string) {
	apiID := 0
	if v := os.Getenv("MYTGFS_API_ID"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			apiID = n
		}
	}
	apiHash := readTrim("secrets/api_hash")
	if apiHash == "" {
		fatal(i18n.T("err.missing_api_hash"))
	}
	return apiID, apiHash
}

func usage() {
	if ui.JSONMode() {
		ui.Error("usage", fmt.Errorf("mytgfs <command> [flags]"))
		os.Exit(2)
	}
	fmt.Fprintln(os.Stderr, i18n.T("cli.usage"))
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
	fmt.Fprintln(os.Stderr, "FAIL:", msg)
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
