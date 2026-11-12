package channel

// 创建频道，以及把 bot 设为频道管理员。

import (
	"context"
	"fmt"

	"github.com/gotd/td/tg"
)

// Create 创建一个私有广播频道。
//
// 输入：ctx；api user 会话的 Telegram API；title 频道名
// 输出：Result
// 预期：不邀请 bot，只建频道
func Create(ctx context.Context, api API, title string) (*Result, error) {
	updates, err := api.ChannelsCreateChannel(ctx, &tg.ChannelsCreateChannelRequest{
		Broadcast: true,
		Title:     title,
		About:     "mytgfs storage",
	})
	if err != nil {
		return nil, fmt.Errorf("建频道: %w", err)
	}
	ch, err := extractChannel(updates)
	if err != nil {
		return nil, err
	}
	return &Result{
		ChannelID:  ch.ID,
		AccessHash: ch.AccessHash,
		Title:      ch.Title,
	}, nil
}

// AddBot 把 bot 设为指定频道的管理员。
//
// 输入：ctx；api user 会话的 Telegram API；res 频道；botUsername 不含 @
// 输出：bot 的 user id
// 预期：bot 不在频道时，editAdmin 会把它加入并授权
func AddBot(ctx context.Context, api API, res *Result, botUsername string) (int64, error) {
	resolved, err := api.ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{
		Username: botUsername,
	})
	if err != nil {
		return 0, fmt.Errorf("解析 bot: %w", err)
	}
	var bot *tg.User
	for _, u := range resolved.Users {
		if user, ok := u.(*tg.User); ok {
			bot = user
			break
		}
	}
	if bot == nil {
		return 0, fmt.Errorf("找不到 bot %s", botUsername)
	}

	rights := tg.ChatAdminRights{
		PostMessages:   true,
		EditMessages:   true,
		DeleteMessages: true,
		PinMessages:    true,
	}
	rights.SetFlags()

	if _, err := api.ChannelsEditAdmin(ctx, &tg.ChannelsEditAdminRequest{
		Channel: &tg.InputChannel{
			ChannelID:  res.ChannelID,
			AccessHash: res.AccessHash,
		},
		UserID:      &tg.InputUser{UserID: bot.ID, AccessHash: bot.AccessHash},
		AdminRights: rights,
	}); err != nil {
		return 0, fmt.Errorf("给 bot 提权: %w", err)
	}
	return bot.ID, nil
}
