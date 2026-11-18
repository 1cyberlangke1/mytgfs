package main

// mytgfs CLI 入口。
//
// 用法：
//   mytgfs auth bot                        bot 登录
//   mytgfs auth user                       手机号登录
//   mytgfs channel init --bot <username>   建私有频道并给 bot 提权
//   mytgfs sync [--limit N]                拉频道历史重建本地索引

import (
	"fmt"
	"os"

	"mytgfs/internal/app"
	"mytgfs/internal/auth"
	"mytgfs/internal/config"
	"mytgfs/internal/i18n"
	"mytgfs/internal/logging"
	"mytgfs/internal/sessionstore"
	"mytgfs/internal/tgclient"
	"mytgfs/internal/ui"
	"mytgfs/internal/version"
)

func main() {
	logging.SetupFromConfig(config.DefaultConfigPath)
	// JSON 输出失败（编码错误）时必须让脚本看到非零退出码
	defer func() {
		if ui.OutputFailed() {
			os.Exit(1)
		}
	}()
	if cfg, err := config.Load(config.DefaultConfigPath); err == nil {
		tgclient.SetDefaultProxy(cfg.Telegram.Proxy)
	}
	os.Args = append([]string{os.Args[0]}, stripJSONFlag(os.Args[1:])...)
	// 相对路径的基准目录：--dir / MYTGFS_DIR，必须在子命令解析之前取走
	os.Args = append([]string{os.Args[0]}, extractDirFlag(os.Args[1:])...)
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
	case "tree":
		runTree(os.Args[2:])
	case "du":
		runDu(os.Args[2:])
	case "cat":
		runCat(os.Args[2:])
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
	case "shell":
		runShell(os.Args[2:])
	case "config":
		runConfig(os.Args[2:])
	case "volume":
		runVolume(os.Args[2:])
	case "web", "tray":
		// 面板与托盘只在完整构建里存在；CLI-only 构建落到未知命令分支
		if !dispatchUI(os.Args[1], os.Args[2:]) {
			usage()
			os.Exit(2)
		}
	case "startup":
		runStartup(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
}

// runAuth 处理 bot / user 登录。

func runAuth(sub string) {
	apiID, apiHash := loadAPI()
	key := mustSessionKey()

	ctx, cancel := interruptContext()
	defer cancel()

	switch sub {
	case "bot":
		token := readTrim(config.DefaultBotTokenFile)
		if token == "" {
			fmt.Fprint(os.Stderr, i18n.T("prompt.bot_token"))
			token = readLine()
		}
		p := auth.Params{
			APIID:   apiID,
			APIHash: apiHash,
			Session: &sessionstore.Encrypted{Path: app.DefaultSessionPath, Key: key},
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
			Session: &sessionstore.Encrypted{Path: app.DefaultUserSessionPath, Key: key},
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
