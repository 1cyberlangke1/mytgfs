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
	"strings"

	"mytgfs/internal/app"
	"mytgfs/internal/channel"
	"mytgfs/internal/config"
	"mytgfs/internal/i18n"
	"mytgfs/internal/sessionstore"
	"mytgfs/internal/tgclient"
	"mytgfs/internal/ui"
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
	cfgPath := fs.String("config", config.DefaultConfigPath, i18n.T("cli.flag.config_file"))
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

	ctx, cancel := interruptContext()
	defer cancel()

	client := tgclient.New(cfg.Telegram.APIID, apiHash,
		&sessionstore.Encrypted{Path: app.DefaultSessionPath, Key: mustSessionKey()},
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
				reason := checkFailure(v.ChatID, cerr)
				results = append(results, volResult{Name: v.Name, ChatID: v.ChatID, Error: reason.Error()})
				ui.Printf("%s\n", i18n.T("volume.check_fail", reason))
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

// checkFailure 把「频道不存在 / bot 已被移出」这类 RPC 错误换成可操作的说明。
//
// 输入：频道号；channel.Check 返回的错误
// 输出：可直接展示的 error；非频道失效类错误原样返回
// 预期：只改写 access_hash 类错误，避免把限流、权限等原因误报成频道已删
func checkFailure(channelID int64, err error) error {
	if channel.IsStale(err) || errors.Is(err, channel.ErrChannelNotVisible) {
		return errors.New(i18n.T("err.channel_unusable", channelID))
	}
	return err
}

// volumeTarget 解析卷名并加载配置。
//
// 输入：cfgPath 配置文件路径；rawName 命令行 --name
// 输出：加载好的 App 与规整后的卷名
// 预期：卷名为空或配置读不出来时直接退出 1
func volumeTarget(cfgPath, rawName string) (*app.App, string) {
	volName := strings.TrimSpace(rawName)
	if volName == "" {
		fatalCode("volume_name_required", i18n.T("err.volume_name_required"))
	}
	a, err := app.Load(app.Options{ConfigPath: cfgPath})
	if err != nil {
		fatal(fmt.Sprintf("%s: %v", i18n.T("err.config_read"), err))
	}
	return a, volName
}

// runVolumeDelete 删除一个卷：删 Telegram 频道 → 移除配置条目 → 删本地索引。
//
// 输入：--name 卷名（必填）；--config 配置文件
// 输出：打印删除结果
// 预期：频道删除失败时中止，不改本地配置；一旦频道删掉，本地清理失败也继续
func runVolumeDelete(args []string) {
	fs := flag.NewFlagSet("volume delete", flag.ExitOnError)
	name := fs.String("name", "", i18n.T("cli.flag.volume_plain"))
	cfgPath := fs.String("config", config.DefaultConfigPath, i18n.T("cli.flag.config_file"))
	_ = fs.Parse(args)

	appRoot, volName := volumeTarget(*cfgPath, *name)

	ctx, cancel := interruptContext()
	defer cancel()

	res, err := appRoot.DeleteVolume(ctx, volName)
	switch {
	case errors.As(err, new(app.ErrVolumeNotFound)):
		fatalCode("volume_not_found", i18n.T("err.volume_not_found", volName))
	case err != nil:
		failErr(i18n.T("err.volume_delete_prefix"), err)
	case res.ChannelDeleted:
		ui.Out(i18n.T("volume.channel_deleted", res.ChannelID))
	default:
		// 频道本来就不存在：删卷把它们清掉就完事
		ui.Out(i18n.T("volume.channel_already_gone", res.ChannelID))
	}
	ui.Out(i18n.T("volume.deleted", res.Name))
}

// runVolumeAttach 把已有频道挂成卷。
//
// 输入：--name 卷名（必填）；--chat-id Bot API 形式 -100... 或裸频道 id；
//
//	--config 配置文件
//
// 输出：卷信息与写入结果
// 预期：只读校验频道可见 + bot 有发言权；频道不存在或无权限直接失败，不写配置
func runVolumeAttach(args []string) {
	fs := flag.NewFlagSet("volume attach", flag.ExitOnError)
	name := fs.String("name", "", i18n.T("cli.flag.volume_index"))
	chatID := fs.Int64("chat-id", 0, i18n.T("cli.flag.chat_id"))
	cfgPath := fs.String("config", config.DefaultConfigPath, i18n.T("cli.flag.config_file"))
	_ = fs.Parse(args)

	appRoot, volName := volumeTarget(*cfgPath, *name)
	ctx, cancel := interruptContext()
	defer cancel()

	res, err := appRoot.AttachVolume(ctx, volName, *chatID)
	switch {
	case errors.Is(err, config.ErrVolumeExists):
		fatalCode("volume_exists", i18n.T("err.volume_exists", volName))
	case errors.Is(err, config.ErrChannelAttached):
		fatalCode("channel_attached", err.Error())
	case err != nil:
		failErr(i18n.T("err.volume_attach_prefix"), err)
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
// 输入：--name 卷名（必填）；--title 频道标题；
//
//	--bot bot 用户名（默认从 bot.session 读）；--config 配置文件
//
// 输出：打印频道 id / chat_id / 是否已静音 / 配置写入结果
// 预期：Telegram 侧建好后追加 [[volumes]]；同名卷直接失败，不会建第二个频道
func runVolumeCreate(args []string) {
	fs := flag.NewFlagSet("volume create", flag.ExitOnError)
	name := fs.String("name", "", i18n.T("cli.flag.volume_index"))
	title := fs.String("title", "", i18n.T("cli.flag.title_default"))
	bot := fs.String("bot", "", i18n.T("cli.flag.bot_default"))
	cfgPath := fs.String("config", config.DefaultConfigPath, i18n.T("cli.flag.config_file"))
	_ = fs.Parse(args)

	appRoot, volName := volumeTarget(*cfgPath, *name)
	ctx, cancel := interruptContext()
	defer cancel()

	res, err := appRoot.CreateVolume(ctx, volName, *title, *bot)
	switch {
	case errors.Is(err, config.ErrVolumeExists):
		fatalCode("volume_exists", i18n.T("err.volume_exists"))
	case err != nil:
		failErr(i18n.T("err.volume_create_prefix"), err)
	}

	ui.Out(i18n.T("volume.ok", res.Name, res.ChannelID, res.ChatID))
	switch {
	case res.Muted:
		ui.Out(i18n.T("volume.muted"))
	case res.MuteErr != nil:
		ui.Out(i18n.T("volume.mute_failed", res.MuteErr))
	}
	ui.Out(i18n.T("volume.saved", res.Name))
	ui.Out(i18n.T("volume.restart_hint"))
}
