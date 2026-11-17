package app

// 建卷：Telegram 侧建频道 → 写配置。
//
// CLI 的 volume create、`mytgfs web` 面板、桌面/安卓面板都调用这里，
// 三边不再各自拼一遍「重名检查 → 读 bot 用户名 → 开 user 连接 → 写配置」。

import (
	"context"
	"errors"
	"strings"

	"github.com/gotd/td/tg"

	"mytgfs/internal/auth"
	"mytgfs/internal/config"
	"mytgfs/internal/i18n"
	"mytgfs/internal/logging"
	"mytgfs/internal/tgclient"
	"mytgfs/internal/volume"
)

// VolumeCreateResult 是建卷结果，字段直接用于命令行与面板展示。
type VolumeCreateResult struct {
	Name      string `json:"name"`
	ChannelID int64  `json:"channel_id"`
	// ChatID 是 Bot API 风格的 -100... 形式
	ChatID int64  `json:"chat_id"`
	Title  string `json:"title"`
	// Muted 表示频道通知已成功关闭；关闭失败不影响卷可用
	Muted bool `json:"muted"`
	// MuteErr 是关通知失败的原因，命令行据此打印具体原因
	MuteErr error `json:"-"`
}

// CreateVolume 新建一个卷并写进配置。
//
// 输入：ctx；name 卷名（必填）；title 频道标题（空则用卷名）；
//
//	botUsername 不含 @，空则从 bot 会话读
//
// 输出：建卷结果；重名返回 config.ErrVolumeExists，缺 user 会话返回 ErrUserSessionMissing
// 预期：
//   - 重名在建频道之前挡掉，不建孤儿频道
//   - 建完才发现请求已取消时删掉刚建的频道，不写配置
//   - 成功后追加 [[volumes]]；默认头像失败只记日志
func (a *App) CreateVolume(ctx context.Context, name, title, botUsername string) (VolumeCreateResult, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return VolumeCreateResult{}, errors.New(i18n.T("err.volume_name_required"))
	}
	if _, ok := a.findConfiguredVolume(name); ok {
		return VolumeCreateResult{}, config.ErrVolumeExists
	}
	botName := strings.TrimPrefix(strings.TrimSpace(botUsername), "@")
	if botName == "" {
		read, err := a.botUsername(ctx)
		if err != nil {
			return VolumeCreateResult{}, err
		}
		botName = read
	}
	if botName == "" {
		return VolumeCreateResult{}, errors.New(i18n.T("err.bot_not_ready"))
	}

	var res volume.Result
	err := a.withUserAPI(ctx, func(ctx context.Context, api *tg.Client) error {
		r, cerr := volume.Create(ctx, api, volume.Request{Name: name, Title: title, BotUsername: botName})
		res = r
		if cerr != nil {
			return cerr
		}
		// 建完频道后才发现已取消：先删频道，不能写进配置留半挂状态
		if ctx.Err() != nil {
			_ = volume.CleanupCancelled(ctx, api, res)
			return ctx.Err()
		}
		return a.MutateConfig(func(p string) error {
			return config.AppendVolume(p, config.Volume{
				Name:    res.Name,
				ChatID:  res.ChannelID,
				Enabled: true,
			})
		})
	})
	if err != nil {
		return VolumeCreateResult{}, err
	}
	if res.AvatarErr != nil {
		logging.Warn("log.cover_set_failed", res.AvatarErr, res.Name)
	}
	return VolumeCreateResult{
		Name:      res.Name,
		ChannelID: res.ChannelID,
		ChatID:    res.ChatID,
		Title:     res.Title,
		Muted:     res.Muted,
		MuteErr:   res.MuteErr,
	}, nil
}

// botUsername 用 bot 会话读取 bot 用户名。
//
// 输入：ctx
// 输出：不含 @ 的用户名；会话未登录或 bot 无用户名时返回 error
// 预期：临时开一条 bot 连接，读完整即退出
func (a *App) botUsername(ctx context.Context) (string, error) {
	var name string
	err := a.withBotAPI(ctx, func(ctx context.Context, c *tgclient.Client) error {
		n, uerr := auth.Username(ctx, c.Client)
		name = n
		return uerr
	})
	if err != nil {
		return "", err
	}
	return strings.TrimPrefix(name, "@"), nil
}
