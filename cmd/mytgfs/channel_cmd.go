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
	"mytgfs/internal/channel"
	"mytgfs/internal/config"
	"mytgfs/internal/crypto"
	"mytgfs/internal/i18n"
	"mytgfs/internal/sessionstore"
	"mytgfs/internal/tgclient"
	"mytgfs/internal/ui"
	"os"
	"os/signal"
	"strconv"
	"strings"
)

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
