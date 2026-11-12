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
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
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
	"mytgfs/internal/i18n"
	"mytgfs/internal/index"
	"mytgfs/internal/ipc"
	"mytgfs/internal/logging"
	"mytgfs/internal/sessionstore"
	tgsync "mytgfs/internal/sync"
	"mytgfs/internal/telegramstore"
	"mytgfs/internal/tgclient"
	"mytgfs/internal/vfs"
	"mytgfs/internal/web"
)

func main() {
	initLogging()
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
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
	case "mount":
		runMount(os.Args[2:])
	case "umount":
		runUmount(os.Args[2:])
	case "daemon":
		runDaemon(os.Args[2:])
	case "web":
		runWeb(os.Args[2:])
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
		r, err := channel.Create(ctx, client.Client, *name)
		if err != nil {
			return err
		}
		if *bot != "" {
			id, err := channel.AddBot(ctx, client.Client, r, *bot)
			if err != nil {
				return err
			}
			r.BotID = id
		}
		res = r
		return nil
	})
	if err != nil {
		fatal(fmt.Sprintf("%s: %v", i18n.T("err.channel_init_prefix"), err))
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

	err = client.Run(ctx, func(ctx context.Context) error {
		items, err := channel.List(ctx, client.Client)
		if err != nil {
			return err
		}
		for _, it := range items {
			fmt.Printf("%d\t%s\t%s\tcreator=%v\tadmin=%v\n", it.ID, it.Title, it.Username, it.Creator, it.Admin)
		}
		return nil
	})
	if err != nil {
		fatal(fmt.Sprintf("列出频道失败: %v", err))
	}
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
		r, err := channel.Add(ctx, client.Client, *channelID, *bot)
		res = r
		return err
	})
	if err != nil {
		fatal(fmt.Sprintf("添加 bot 失败: %v", err))
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
		r, err := channel.Init(ctx, client.Client, *botUsername, *name)
		res = r
		return err
	})
	if err != nil {
		fatal(fmt.Sprintf("%s: %v", i18n.T("err.channel_init_prefix"), err))
	}
	fmt.Println(i18n.T("channel.ok", res.ChannelID, res.AccessHash, chatID(res.ChannelID)))
}

// runSync 增量拉取频道消息并更新本地索引。
func runSync(args []string) {
	fs := flag.NewFlagSet("sync", flag.ExitOnError)
	limit := fs.Int("limit", 200, "messages to fetch")
	name := fs.String("name", "mytgfs", "volume name")
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
		fatal(fmt.Sprintf("同步失败: %v", err))
	}

	entries, err := idx.List(ctx, "/")
	if err != nil {
		fatal(fmt.Sprintf("读取索引: %v", err))
	}
	fmt.Printf("OK synced, root entries=%d\n", len(entries))
}
// runMount 挂载一个或多个卷；每个群组一个盘。
func runMount(args []string) {
	fs := flag.NewFlagSet("mount", flag.ExitOnError)
	point := fs.String("point", "", "mount point")
	_ = fs.Parse(args)

	cfg, err := config.Load("config.toml")
	if err != nil {
		fatal(fmt.Sprintf("读取配置: %v", err))
	}
	if *point == "" {
		*point = cfg.Mount.Point
	}
	apiHash := cfg.APIHash()
	if apiHash == "" {
		fatal(i18n.T("err.missing_api_hash"))
	}
	key, err := crypto.LoadOrCreateKey("secrets/session.key")
	if err != nil {
		fatal(fmt.Sprintf("%s: %v", i18n.T("err.session_key_prefix"), err))
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	client := tgclient.New(cfg.Telegram.APIID, apiHash,
		&sessionstore.Encrypted{Path: "secrets/bot.session", Key: key},
		tgclient.Options{})

	err = client.Run(ctx, func(ctx context.Context) error {
		var wg sync.WaitGroup
		defer wg.Wait()

		token, err := newToken()
		if err != nil {
			return err
		}
		srv, err := ipc.Listen(token)
		if err != nil {
			return err
		}
		defer srv.Close()

		var ctlMu sync.Mutex
		cancels := map[string]context.CancelFunc{}
		srv.SetHandler(func(_ context.Context, action string, data json.RawMessage) (any, error) {
			if action != "umount" {
				return nil, fmt.Errorf("未知操作: %s", action)
			}
			var req struct {
				Point string `json:"point"`
			}
			if len(data) > 0 {
				_ = json.Unmarshal(data, &req)
			}
			ctlMu.Lock()
			defer ctlMu.Unlock()
			if req.Point == "" {
				for _, c := range cancels {
					c()
				}
				return "all", nil
			}
			c, ok := cancels[req.Point]
			if !ok {
				return nil, fmt.Errorf("未挂载: %s", req.Point)
			}
			delete(cancels, req.Point)
			c()
			return req.Point, nil
		})
		go func() { _ = srv.Serve(ctx) }()

		mountOne := func(name string, channelID int64, mp string) {
			if mp == "" {
				fatal("卷 " + name + " 缺少挂载点")
			}
			idx, err := index.Open(datadir.Path("index-" + name + ".db"))
			if err != nil {
				fatal(fmt.Sprintf("打开索引 %s: %v", name, err))
			}
			store := telegramstore.New(client.API(), channelID)
			vctx, vcancel := context.WithCancel(ctx)
			fsys := vfs.New(vctx, idx, store, os.TempDir())
			ctlMu.Lock()
			cancels[mp] = vcancel
			ctlMu.Unlock()
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer idx.Close()
				defer vcancel()
				if err := vfs.Mount(vctx, fsys, mp, name); err != nil {
					fmt.Fprintf(os.Stderr, "挂载 %s 失败: %v\n", mp, err)
				}
				ctlMu.Lock()
				delete(cancels, mp)
				empty := len(cancels) == 0
				ctlMu.Unlock()
				if empty {
					cancel()
				}
			}()
			fmt.Printf("mounted %s at %s\n", name, mp)
		}

		if len(cfg.Volumes) == 0 {
			mountOne("mytgfs", cfg.Telegram.ChannelID, *point)
		} else {
			for _, v := range cfg.Volumes {
				if !v.Enabled {
					continue
				}
				mp := v.Point
				if mp == "" {
					mp = *point
				}
				mountOne(v.Name, v.ChatID, mp)
			}
		}
		ctlMu.Lock()
		points := make([]string, 0, len(cancels))
		for p := range cancels {
			points = append(points, p)
		}
		ctlMu.Unlock()
		if err := writeMountState(mountState{Addr: srv.Addr(), Token: token, Points: points}); err != nil {
			return fmt.Errorf("写挂载状态: %w", err)
		}
		defer removeMountState()

		<-ctx.Done()
		return nil
	})
	if err != nil {
		fatal(fmt.Sprintf("挂载失败: %v", err))
	}
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
		openBrowser(url)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if err := web.Serve(ctx, addr, "config.toml"); err != nil {
		fatal(err.Error())
	}
}

// openBrowser 尝试用系统默认浏览器打开 URL。
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
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

func fatal(msg string) {
	fmt.Fprintln(os.Stderr, "FAIL:", msg)
	os.Exit(1)
}
