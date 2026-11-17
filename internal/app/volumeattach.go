package app

// 挂载已有频道：只读校验 → 写配置。
//
// CLI 的 volume attach、`mytgfs web` 面板、桌面/安卓面板都调用这里。

import (
	"context"
	"errors"
	"strings"

	"mytgfs/internal/config"
	"mytgfs/internal/i18n"
	"mytgfs/internal/tgclient"
	"mytgfs/internal/volume"
)

// VolumeAttachResult 是挂载已有频道的结果。
type VolumeAttachResult struct {
	Name      string `json:"name"`
	ChannelID int64  `json:"channel_id"`
	// ChatID 是 Bot API 风格的 -100... 形式
	ChatID int64  `json:"chat_id"`
	Title  string `json:"title"`
}

// AttachVolume 把一个已经存在的频道挂成卷。
//
// 输入：ctx；name 卷名（必填）；chatID Bot API 形式 -100... 或裸频道 id
// 输出：挂载结果；重名返回 config.ErrVolumeExists，频道已挂过返回 config.ErrChannelAttached
// 预期：
//   - 只读校验（bot 能看见频道且有发言权），不往频道写任何东西
//   - 成功后追加 [[volumes]]；失败时配置保持原样
func (a *App) AttachVolume(ctx context.Context, name string, chatID int64) (VolumeAttachResult, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return VolumeAttachResult{}, errors.New(i18n.T("err.volume_name_required"))
	}
	if _, ok := a.findConfiguredVolume(name); ok {
		return VolumeAttachResult{}, config.ErrVolumeExists
	}
	norm := volume.NormalizeChatID(chatID)
	if norm == 0 {
		return VolumeAttachResult{}, errors.New(i18n.T("err.volume_chat_id_invalid"))
	}
	for _, v := range a.Config().Volumes {
		if volume.NormalizeChatID(v.ChatID) == norm {
			return VolumeAttachResult{}, config.ErrChannelAttached
		}
	}

	var res volume.Result
	err := a.withBotAPI(ctx, func(ctx context.Context, c *tgclient.Client) error {
		r, aerr := volume.Attach(ctx, c.API(), volume.AttachRequest{Name: name, ChatID: chatID})
		res = r
		return aerr
	})
	if err != nil {
		return VolumeAttachResult{}, err
	}
	if werr := a.MutateConfig(func(p string) error {
		return config.AppendVolume(p, config.Volume{
			Name:    res.Name,
			ChatID:  res.ChannelID,
			Enabled: true,
		})
	}); werr != nil {
		return VolumeAttachResult{}, werr
	}
	return VolumeAttachResult{
		Name:      res.Name,
		ChannelID: res.ChannelID,
		ChatID:    res.ChatID,
		Title:     res.Title,
	}, nil
}
