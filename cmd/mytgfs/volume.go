package main

// `mytgfs volume` 子命令：建卷并把卷写进配置。
//
// 建卷 = 建私有频道 + 拉 bot 进频道提权 + 永久关闭频道通知；
// 频道的创建/提权只有 user 会话能做，所以这里用 user.session，
// bot 用户名默认从 bot.session 读，不需要用户手输。

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"

	"mytgfs/internal/auth"
	"mytgfs/internal/channel"
	"mytgfs/internal/config"
	"mytgfs/internal/crypto"
	"mytgfs/internal/datadir"
	"mytgfs/internal/i18n"
	"mytgfs/internal/sessionstore"
	"mytgfs/internal/tgclient"
	"mytgfs/internal/ui"
	"mytgfs/internal/volume"
)

// runVolume 分发卷相关子命令。
//
// 输入：args 子命令及其参数
// 输出：无；未知子命令打印用法并退出 2
func runVolume(args []string) {
	if len(args) == 0 {
		usage()
		os.Exit(2)
	}
	switch args[0] {
	case "attach":
		runVolumeAttach(args[1:])
	case "create":
		runVolumeCreate(args[1:])
	case "delete":
		runVolumeDelete(args[1:])
	case "check":
		runVolumeCheck(args[1:])
	default:
		usage()
		os.Exit(2)
	}
}

// runVolumeCheck 用 bot 会话逐个检查卷的频道是否仍可用。
//
// 输入：--config 配置文件
// 输出：每个卷一行状态，最后一行汇总；有卷不可用时退出码 1
// 预期：只读 RPC（getChannels + getParticipant），不往频道写任何消息
func runVolumeCheck(args []string) {
	fs := flag.NewFlagSet("volume check", flag.ExitOnError)
	cfgPath := fs.String("config", "config.toml", i18n.T("cli.flag.config_file"))
	_ = fs.Parse(args)

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fatal(fmt.Sprintf("%s: %v", i18n.T("err.config_load"), err))
	}
	if len(cfg.Volumes) == 0 {
		ui.Out(i18n.T("volume.check_none"))
		return
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
	bad := 0
	type volResult struct {
		Name   string `json:"name"`
		ChatID int64  `json:"chat_id"`
		OK     bool   `json:"ok"`
		Error  string `json:"error,omitempty"`
	}
	results := make([]volResult, 0, len(cfg.Volumes))
	err = client.Run(ctx, func(ctx context.Context) error {
		for _, v := range cfg.Volumes {
			ui.Printf("%s\n", i18n.T("volume.check_title", v.Name, v.ChatID))
			if _, cerr := channel.Check(ctx, client.API(), v.ChatID); cerr != nil {
				bad++
				results = append(results, volResult{Name: v.Name, ChatID: v.ChatID, Error: cerr.Error()})
				ui.Printf("%s\n", i18n.T("volume.check_fail", cerr))
				continue
			}
			results = append(results, volResult{Name: v.Name, ChatID: v.ChatID, OK: true})
			ui.Printf("%s\n", i18n.T("volume.check_ok"))
		}
		return nil
	})
	if err != nil {
		failErr(i18n.T("err.channel_check_prefix"), err)
	}
	ui.Printf("%s\n", i18n.T("volume.check_summary", len(cfg.Volumes), bad))
	ui.Print(map[string]any{"volumes": results, "bad": bad})
	if bad > 0 {
		os.Exit(1)
	}
}

// runVolumeDelete 删除一个卷：删 Telegram 频道 → 移除配置条目 → 删本地索引。
//
// 输入：--name 卷名（必填）；--config 配置文件
// 输出：打印删除结果
// 预期：频道删除失败时中止，不改本地配置；一旦频道删掉，本地清理失败也继续
func runVolumeDelete(args []string) {
	fs := flag.NewFlagSet("volume delete", flag.ExitOnError)
	name := fs.String("name", "", i18n.T("cli.flag.volume_plain"))
	cfgPath := fs.String("config", "config.toml", i18n.T("cli.flag.config_file"))
	_ = fs.Parse(args)

	volName := strings.TrimSpace(*name)
	if volName == "" {
		fatal(i18n.T("err.volume_name_required"))
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fatal(fmt.Sprintf("%s: %v", i18n.T("err.config_read"), err))
	}
	var target *config.Volume
	for i := range cfg.Volumes {
		if strings.EqualFold(cfg.Volumes[i].Name, volName) {
			target = &cfg.Volumes[i]
			break
		}
	}
	if target == nil {
		fatal(i18n.T("err.volume_not_found", volName))
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

	channelID := target.ChatID

	client := tgclient.New(cfg.Telegram.APIID, apiHash,
		&sessionstore.Encrypted{Path: "secrets/user.session", Key: key},
		tgclient.Options{})
	err = client.Run(ctx, func(ctx context.Context) error {
		res, rerr := channel.Resolve(ctx, client.API(), channelID)
		if rerr != nil {
			return rerr
		}
		return channel.Delete(ctx, client.API(), res)
	})
	if err != nil {
		failErr(i18n.T("err.volume_delete_prefix"), err)
	}
	ui.Out(i18n.T("volume.channel_deleted", channelID))

	if err := config.RemoveVolume(*cfgPath, volName); err != nil {
		failErr(i18n.T("err.volume_config_remove_prefix"), err)
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		p := datadir.Path("index-" + volName + ".db" + suffix)
		if rmErr := os.Remove(p); rmErr != nil && !os.IsNotExist(rmErr) {
			ui.Out(i18n.T("volume.index_remove_failed", p, rmErr))
		}
	}
	ui.Out(i18n.T("volume.deleted", volName))
}

// runVolumeAttach 把已有频道挂成卷。
//
// 输入：--name 卷名（必填）；--chat-id Bot API 形式 -100... 或裸频道 id；
//
//	--point 盘符；--config 配置文件
//
// 输出：卷信息与写入结果
// 预期：只读校验频道可见 + bot 有发言权；频道不存在或无权限直接失败，不写配置
func runVolumeAttach(args []string) {
	fs := flag.NewFlagSet("volume attach", flag.ExitOnError)
	name := fs.String("name", "", i18n.T("cli.flag.volume_index"))
	chatID := fs.Int64("chat-id", 0, i18n.T("cli.flag.chat_id"))
	point := fs.String("point", "", i18n.T("cli.flag.mount_example"))
	cfgPath := fs.String("config", "config.toml", i18n.T("cli.flag.config_file"))
	_ = fs.Parse(args)

	volName := strings.TrimSpace(*name)
	if volName == "" {
		fatal(i18n.T("err.volume_name_required"))
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fatal(fmt.Sprintf("%s: %v", i18n.T("err.config_read"), err))
	}
	for _, v := range cfg.Volumes {
		if strings.EqualFold(v.Name, volName) {
			fatal(i18n.T("err.volume_exists", volName))
		}
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

	botClient := tgclient.New(cfg.Telegram.APIID, apiHash,
		&sessionstore.Encrypted{Path: "secrets/bot.session", Key: key}, tgclient.Options{})
	var res volume.Result
	err = botClient.Run(ctx, func(ctx context.Context) error {
		r, aerr := volume.Attach(ctx, botClient.API(), volume.AttachRequest{Name: volName, ChatID: *chatID})
		res = r
		return aerr
	})
	if err != nil {
		failErr(i18n.T("err.volume_attach_prefix"), err)
	}
	vol := config.Volume{
		Name:    res.Name,
		ChatID:  res.ChannelID,
		Point:   strings.TrimSpace(*point),
		Enabled: true,
	}
	if aerr := config.AppendVolume(*cfgPath, vol); aerr != nil {
		failErr(i18n.T("err.volume_config_append_prefix"), aerr)
	}
	ui.Print(map[string]any{
		"name":       res.Name,
		"channel_id": res.ChannelID,
		"chat_id":    res.ChatID,
		"title":      res.Title,
	})
}

// runVolumeCreate 新建一个卷并写入配置文件。
//
// 输入：--name 卷名（必填）；--title 频道标题；--point 挂载点；
//
//	--bot bot 用户名（默认从 bot.session 读）；--config 配置文件
//
// 输出：打印频道 id / chat_id / 是否已静音 / 配置写入结果
// 预期：Telegram 侧建好后追加 [[volumes]]；同名卷直接失败，不会建第二个频道
func runVolumeCreate(args []string) {
	fs := flag.NewFlagSet("volume create", flag.ExitOnError)
	name := fs.String("name", "", i18n.T("cli.flag.volume_index"))
	title := fs.String("title", "", i18n.T("cli.flag.title_default"))
	point := fs.String("point", "", i18n.T("cli.flag.mount_example"))
	bot := fs.String("bot", "", i18n.T("cli.flag.bot_default"))
	cfgPath := fs.String("config", "config.toml", i18n.T("cli.flag.config_file"))
	_ = fs.Parse(args)

	volName := strings.TrimSpace(*name)
	if volName == "" {
		fatal(i18n.T("err.volume_name_required"))
	}
	// 先查配置里有没有重名，避免白建一个频道
	if cfg, err := config.Load(*cfgPath); err == nil {
		for _, v := range cfg.Volumes {
			if strings.EqualFold(v.Name, volName) {
				fatal(i18n.T("err.volume_exists"))
			}
		}
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fatal(fmt.Sprintf("%s: %v", i18n.T("err.config_read"), err))
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

	botName := strings.TrimPrefix(strings.TrimSpace(*bot), "@")
	if botName == "" {
		botName = readBotUsername(ctx, cfg, apiHash, key)
	}

	userClient := tgclient.New(cfg.Telegram.APIID, apiHash,
		&sessionstore.Encrypted{Path: "secrets/user.session", Key: key},
		tgclient.Options{})

	var res volume.Result
	err = userClient.Run(ctx, func(ctx context.Context) error {
		r, cerr := volume.Create(ctx, userClient.API(), volume.Request{
			Name:        volName,
			Title:       *title,
			BotUsername: botName,
		})
		res = r
		return cerr
	})
	if err != nil {
		failErr(i18n.T("err.volume_create_prefix"), err)
	}

	vol := config.Volume{
		Name:    res.Name,
		ChatID:  res.ChannelID,
		Point:   strings.TrimSpace(*point),
		Enabled: true,
	}
	if aerr := config.AppendVolume(*cfgPath, vol); aerr != nil {
		if errors.Is(aerr, config.ErrVolumeExists) {
			fatal(i18n.T("err.volume_exists"))
		}
		fatal(fmt.Sprintf("%s: %v", i18n.T("err.config_write"), aerr))
	}

	ui.Out(i18n.T("volume.ok", res.Name, res.ChannelID, res.ChatID))
	switch {
	case res.Muted:
		ui.Out(i18n.T("volume.muted"))
	case res.MuteErr != nil:
		ui.Out(i18n.T("volume.mute_failed", res.MuteErr))
	}
	mountPoint := vol.Point
	if mountPoint == "" {
		mountPoint = "-"
	}
	ui.Out(i18n.T("volume.saved", vol.Name, mountPoint))
	ui.Out(i18n.T("volume.restart_hint"))
}

// readBotUsername 用 bot 会话读取 bot 用户名。
//
// 输入：ctx；cfg 配置；apiHash；key 会话密钥
// 输出：不含 @ 的用户名
// 预期：bot 没登录/会话失效时直接退出，并提示重新 auth bot
func readBotUsername(ctx context.Context, cfg config.Config, apiHash string, key []byte) string {
	c := tgclient.New(cfg.Telegram.APIID, apiHash,
		&sessionstore.Encrypted{Path: "secrets/bot.session", Key: key},
		tgclient.Options{})
	var name string
	err := c.Run(ctx, func(ctx context.Context) error {
		n, uerr := auth.Username(ctx, c.Client)
		name = n
		return uerr
	})
	if err != nil {
		failErr(i18n.T("err.bot_login_prefix"), err)
	}
	return name
}
