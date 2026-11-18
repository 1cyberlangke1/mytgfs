package main

// mytgfs CLI 入口。
//
// 用法：
//   mytgfs auth bot                        bot 登录
//   mytgfs auth user                       手机号登录
//   mytgfs channel init --bot <username>   建私有频道并给 bot 提权
//   mytgfs sync [--limit N]                拉频道历史重建本地索引

import (
	"cmp"
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/gotd/td/tg"

	"mytgfs/internal/channel"
	"mytgfs/internal/config"
	"mytgfs/internal/i18n"
	"mytgfs/internal/ui"
)

// runChannelCreate 建私有频道，可选把 bot 提为管理员。
//
// 输入：--name 频道标题；--bot bot 用户名（可空）
// 输出：频道 id / access_hash / chat_id
// 预期：建频道与拉 bot 都在同一条 user 连接里完成，任一失败即整体失败
func runChannelCreate(args []string) {
	fs := flag.NewFlagSet("channel create", flag.ExitOnError)
	name := fs.String("name", "mytgfs", i18n.T("cli.flag.channel_title"))
	bot := fs.String("bot", "", i18n.T("cli.flag.bot_optional"))
	_ = fs.Parse(args)

	var res *channel.Result
	err := runUserSession(func(ctx context.Context, api *tg.Client) error {
		r, cerr := channel.Create(ctx, api, *name)
		if cerr != nil {
			return cerr
		}
		if *bot != "" {
			id, aerr := channel.AddBot(ctx, api, r, *bot)
			if aerr != nil {
				return aerr
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
//
// 输入：无额外参数
// 输出：每行一个频道的可读列表，--json 时输出结构化结果
// 预期：只读 RPC，不改动任何频道
func runChannelList(args []string) {
	fs := flag.NewFlagSet("channel list", flag.ExitOnError)
	_ = fs.Parse(args)

	var out []channel.Item
	err := runUserSession(func(ctx context.Context, api *tg.Client) error {
		items, lerr := channel.List(ctx, api)
		if lerr != nil {
			return lerr
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

// runChannelAdd 把 bot 加到已有频道并提权。
//
// 输入：--channel 频道 id；--bot bot 用户名（都不为空）
// 输出：频道 id / access_hash / chat_id
// 预期：缺参数直接退出 1，不发 RPC
func runChannelAdd(args []string) {
	fs := flag.NewFlagSet("channel add", flag.ExitOnError)
	channelID := fs.Int64("channel", 0, "channel id")
	bot := fs.String("bot", "", i18n.T("cli.flag.bot"))
	_ = fs.Parse(args)

	if *channelID == 0 || *bot == "" {
		fatal(i18n.T("err.need_channel_bot"))
	}

	var res *channel.Result
	err := runUserSession(func(ctx context.Context, api *tg.Client) error {
		r, cerr := channel.Add(ctx, api, *channelID, *bot)
		res = r
		return cerr
	})
	if err != nil {
		failErr(i18n.T("err.add_bot"), err)
	}
	ui.Out(i18n.T("channel.ok", res.ChannelID, res.AccessHash, chatID(res.ChannelID)))
}

// runChannelInit 建频道并给 bot 提权（等价 channel create --bot）。
//
// 输入：--name 频道标题；--bot bot 用户名（必填）
// 输出：频道 id / access_hash / chat_id
// 预期：--bot 为空直接退出 1
func runChannelInit(args []string) {
	fs := flag.NewFlagSet("channel init", flag.ExitOnError)
	name := fs.String("name", "mytgfs", i18n.T("cli.flag.channel_title"))
	botUsername := fs.String("bot", "", i18n.T("cli.flag.bot_no_at"))
	_ = fs.Parse(args)

	if *botUsername == "" {
		fatal(i18n.T("err.bot_required"))
	}

	var res *channel.Result
	err := runUserSession(func(ctx context.Context, api *tg.Client) error {
		r, cerr := channel.Init(ctx, api, *botUsername, *name)
		res = r
		return cerr
	})
	if err != nil {
		failErr(i18n.T("err.channel_init_prefix"), err)
	}
	ui.Out(i18n.T("channel.ok", res.ChannelID, res.AccessHash, chatID(res.ChannelID)))
}

// chatID 把频道 id 转成 Bot API 的 -100... 形式。
//
// 输入：channelID 原始频道 id
// 输出：-100<channelID> 形式的 chat_id
// 预期：只做展示与配置写法，不改动索引里的原始 id
func chatID(channelID int64) int64 {
	return -1000000000000 - channelID
}

// loadAPI 读取 Telegram App 凭据。
//
// 输入：无（读 MYTGFS_API_ID 环境变量与 config.toml）
// 输出：api_id、api_hash
// 预期：api_id 先看环境变量，再看 config.toml 的 [telegram].api_id；
//
//	两者都没有时直接报错退出，不内置任何默认值（api_id 是应用所有者的身份）。
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
		if cfg, err := config.Load(config.DefaultConfigPath); err == nil {
			apiID = cfg.Telegram.APIID
		}
	}
	if apiID == 0 {
		fatal(i18n.T("err.missing_api_id"))
	}
	apiHash := readTrim(config.DefaultAPIHashFile)
	if cfg, err := config.Load(config.DefaultConfigPath); err == nil {
		// 尊重配置里的自定义路径：凭据文件与 config.toml 放一起是常见部署
		apiHash = readTrim(cmp.Or(cfg.Telegram.APIHashFile, config.DefaultAPIHashFile))
	}
	if apiHash == "" {
		fatal(i18n.T("err.missing_api_hash"))
	}
	return apiID, apiHash
}
