package channel

// 频道初始化：用 user 会话建私有频道、拉 bot 进频道、给 bot 提权。
//
// 这三步里 createChannel / inviteToChannel / editAdmin 都是 user-only，
// bot 自己做不到，所以由 user 会话执行。

import (
	"context"
	"fmt"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
)

// Result 是初始化完成后的频道信息。
type Result struct {
	ChannelID  int64
	AccessHash int64
	Title      string
	BotID      int64
}

// Init 创建私有频道并把 bot 设为管理员。
//
// 输入：ctx；client 已登录的 user 客户端；botUsername 不含 @；title 频道名
// 输出：Result
// 预期：频道为 broadcast（私有频道），bot 获得发/改/删/置顶权限
func Init(ctx context.Context, client *telegram.Client, botUsername, title string) (*Result, error) {
	api := client.API()

	// 1. 解析 bot username，拿到 InputUser（邀请需要 access_hash）
	resolved, err := api.ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{
		Username: botUsername,
	})
	if err != nil {
		return nil, fmt.Errorf("解析 bot: %w", err)
	}
	var bot *tg.User
	for _, u := range resolved.Users {
		if user, ok := u.(*tg.User); ok {
			bot = user
			break
		}
	}
	if bot == nil {
		return nil, fmt.Errorf("找不到 bot %s", botUsername)
	}
	botInput := &tg.InputUser{UserID: bot.ID, AccessHash: bot.AccessHash}

	// 2. 建私有频道
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
	chInput := &tg.InputChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash}

	// 3. 直接把 bot 设为管理员。
	// bot 在频道里只能以管理员身份存在，inviteToChannel 会报 USER_BOT，
	// 所以直接用 editAdmin 把它加入并授权。
	rights := tg.ChatAdminRights{
		PostMessages:   true,
		EditMessages:   true,
		DeleteMessages: true,
		PinMessages:    true,
	}
	rights.SetFlags()
	if _, err := api.ChannelsEditAdmin(ctx, &tg.ChannelsEditAdminRequest{
		Channel:     chInput,
		UserID:      botInput,
		AdminRights: rights,
	}); err != nil {
		return nil, fmt.Errorf("给 bot 提权: %w", err)
	}

	return &Result{
		ChannelID:  ch.ID,
		AccessHash: ch.AccessHash,
		Title:      ch.Title,
		BotID:      bot.ID,
	}, nil
}

// extractChannel 从 updates 里取出新建的 channel。
func extractChannel(u tg.UpdatesClass) (*tg.Channel, error) {
	upd, ok := u.(*tg.Updates)
	if !ok {
		return nil, fmt.Errorf("createChannel 返回类型 %T", u)
	}
	for _, c := range upd.Chats {
		if ch, ok := c.(*tg.Channel); ok {
			return ch, nil
		}
	}
	return nil, fmt.Errorf("createChannel 返回里没有 channel")
}
