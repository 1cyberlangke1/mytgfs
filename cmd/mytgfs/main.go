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
	"net"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"time"

	"mytgfs/internal/auth"
	"mytgfs/internal/channel"
	"mytgfs/internal/config"
	"mytgfs/internal/crypto"
	"mytgfs/internal/daemon"
	"mytgfs/internal/i18n"
	"mytgfs/internal/index"
	"mytgfs/internal/ipc"
	"mytgfs/internal/sessionstore"
	tgsync "mytgfs/internal/sync"
	"mytgfs/internal/telegramstore"
	"mytgfs/internal/tgclient"
	"mytgfs/internal/vfs"
	"mytgfs/internal/web"
)

func main() {
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
		if len(os.Args) < 3 || os.Args[2] != "init" {
			usage()
			os.Exit(2)
		}
		runChannelInit(os.Args[3:])
	case "sync":
		runSync(os.Args[2:])
	case "mount":
		runMount(os.Args[2:])
	case "daemon":
		runDaemon(os.Args[2:])
	case "web":
		runWeb(os.Args[2:])
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

// runChannelInit 用 user 会话建频道、拉 bot、提权。
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

// runSync 拉取频道历史并重建本地索引。
func runSync(args []string) {
	fs := flag.NewFlagSet("sync", flag.ExitOnError)
	limit := fs.Int("limit", 200, "messages to fetch")
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
	idx, err := index.Open("index.db")
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

// runMount 挂载 FUSE 文件系统。
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
	if *point == "" {
		fatal("需要 --point 或 config.mount.point")
	}
	apiHash := cfg.APIHash()
	if apiHash == "" {
		fatal(i18n.T("err.missing_api_hash"))
	}
	key, err := crypto.LoadOrCreateKey("secrets/session.key")
	if err != nil {
		fatal(fmt.Sprintf("%s: %v", i18n.T("err.session_key_prefix"), err))
	}
	idx, err := index.Open("index.db")
	if err != nil {
		fatal(fmt.Sprintf("打开索引: %v", err))
	}
	defer idx.Close()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	client := tgclient.New(cfg.Telegram.APIID, apiHash,
		&sessionstore.Encrypted{Path: "secrets/bot.session", Key: key},
		tgclient.Options{})

	err = client.Run(ctx, func(ctx context.Context) error {
		store := telegramstore.New(client.API(), cfg.Telegram.ChannelID)
		fsys := vfs.New(ctx, idx, store, os.TempDir())
		return vfs.Mount(ctx, fsys, *point, "mytgfs")
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
