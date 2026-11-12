package channel

// 关闭当前账号对频道的新消息通知。

import (
	"context"
	"fmt"

	"github.com/gotd/td/tg"
)

// muteForever 是 Telegram 的永久静音标记（0x7FFFFFFF）。
const muteForever = 2147483647

// MuteForever 永久关闭当前账号在指定频道的通知。
//
// 输入：ctx；api user 会话的 Telegram API；res 频道
// 输出：error
// 预期：mute_until 设为 0x7FFFFFFF 表示永久静音；上传文件时手机不再逐条弹提醒
func MuteForever(ctx context.Context, api API, res *Result) error {
	settings := tg.InputPeerNotifySettings{}
	settings.SetShowPreviews(false)
	settings.SetSilent(true)
	settings.SetMuteUntil(muteForever)

	if _, err := api.AccountUpdateNotifySettings(ctx, &tg.AccountUpdateNotifySettingsRequest{
		Peer: &tg.InputNotifyPeer{
			Peer: &tg.InputPeerChannel{
				ChannelID:  res.ChannelID,
				AccessHash: res.AccessHash,
			},
		},
		Settings: settings,
	}); err != nil {
		return fmt.Errorf("关闭频道通知: %w", err)
	}
	return nil
}
