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
	"mytgfs/internal/config"
	"mytgfs/internal/crypto"
	"mytgfs/internal/i18n"
	"mytgfs/internal/sessionstore"
	"mytgfs/internal/tgclient"
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
	case "create":
		runVolumeCreate(args[1:])
	default:
		usage()
		os.Exit(2)
	}
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
	name := fs.String("name", "", "volume name (index-<name>.db)")
	title := fs.String("title", "", "channel title (default: name)")
	point := fs.String("point", "", "mount point, e.g. Y:")
	bot := fs.String("bot", "", "bot username (default: read from bot session)")
	cfgPath := fs.String("config", "config.toml", "config file")
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
		fatal(fmt.Sprintf("写入配置: %v", aerr))
	}

	fmt.Println(i18n.T("volume.ok", res.Name, res.ChannelID, res.ChatID))
	switch {
	case res.Muted:
		fmt.Println(i18n.T("volume.muted"))
	case res.MuteErr != nil:
		fmt.Println(i18n.T("volume.mute_failed", res.MuteErr))
	}
	mountPoint := vol.Point
	if mountPoint == "" {
		mountPoint = "-"
	}
	fmt.Println(i18n.T("volume.saved", vol.Name, mountPoint))
	fmt.Println(i18n.T("volume.restart_hint"))
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
