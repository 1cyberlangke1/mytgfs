package channel

// 创建频道，以及把 bot 设为频道管理员。

import (
	"context"
	"fmt"

	"github.com/gotd/td/tg"

	"mytgfs/internal/i18n"
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
		return nil, fmt.Errorf(i18n.T("err.create_channel")+": %w", err)
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
	bot, err := resolveBot(ctx, api, botUsername)
	if err != nil {
		return 0, err
	}
	if err := grantBot(ctx, api, res.ChannelID, res.AccessHash, bot); err != nil {
		return 0, err
	}
	return bot.ID, nil
}

// resolveBot 用用户名查 bot。
//
// 输入：ctx；api user 会话；botUsername 不含 @
// 输出：bot 的 *tg.User（含 user 会话视角的 access_hash）
// 预期：用户名对不上或返回里没有用户时报错
func resolveBot(ctx context.Context, api GrantAPI, botUsername string) (*tg.User, error) {
	resolved, err := api.ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{
		Username: botUsername,
	})
	if err != nil {
		return nil, fmt.Errorf(i18n.T("err.resolve_bot")+": %w", err)
	}
	for _, u := range resolved.Users {
		if user, ok := u.(*tg.User); ok {
			return user, nil
		}
	}
	return nil, fmt.Errorf(i18n.T("err.bot_not_found"), botUsername)
}

// BotRights 返回 bot 需要的管理员权限。
//
// 输入：无
// 输出：打开 PostMessages/EditMessages/ChangeInfo 且已 SetFlags 的权限
// 预期：只授业务真正调用的三项；删除已改成打标记、置顶模型已废弃，
// 因此不授 DeleteMessages/PinMessages，避免 token 泄漏时放大破坏面
func BotRights() tg.ChatAdminRights {
	rights := tg.ChatAdminRights{
		PostMessages: true, // 发文件与目录节点消息
		EditMessages: true, // 改名、打删除标记
		ChangeInfo:   true, // 改频道标题
	}
	rights.SetFlags()
	return rights
}

// GrantAPI 是补授权需要的最小 API 面。
type GrantAPI interface {
	ChannelsGetChannels(ctx context.Context, req []tg.InputChannelClass) (tg.MessagesChatsClass, error)
	ChannelsEditAdmin(ctx context.Context, req *tg.ChannelsEditAdminRequest) (tg.UpdatesClass, error)
	ContactsResolveUsername(ctx context.Context, req *tg.ContactsResolveUsernameRequest) (*tg.ContactsResolvedPeer, error)
}

// GrantBotByUsername 按用户名把 bot 设为频道管理员。
//
// 输入：ctx；api 需要 AddAdmins 权限（只能是 user 会话）；频道号与 access_hash
//
//	（0 时现解析）；botUsername 不含 @
//
// 输出：error；上游拒绝时返回带提示的错误
// 预期：幂等；用 user 会话自己解析 bot，保证 InputUser 的 access_hash 有效；
//
//	权限没变化时 Telegram 不会报错，可直接用于修补老卷
func GrantBotByUsername(ctx context.Context, api GrantAPI, channelID, accessHash int64, botUsername string) error {
	bot, err := resolveBot(ctx, api, botUsername)
	if err != nil {
		return err
	}
	return grantBot(ctx, api, channelID, accessHash, bot)
}

// grantBot 用已解析的 bot 写管理员权限。
//
// 输入：ctx；api；频道号与 access_hash（0 时现解析）；bot 的 *tg.User
// 输出：error
// 预期：覆盖式设置 BotRights，只动 bot 的管理员权限
func grantBot(ctx context.Context, api GrantAPI, channelID, accessHash int64, bot *tg.User) error {
	if accessHash == 0 {
		resolved, err := resolveChannelAccess(ctx, api, channelID)
		if err != nil {
			return err
		}
		accessHash = resolved
	}
	if _, err := api.ChannelsEditAdmin(ctx, &tg.ChannelsEditAdminRequest{
		Channel:     &tg.InputChannel{ChannelID: channelID, AccessHash: accessHash},
		UserID:      &tg.InputUser{UserID: bot.ID, AccessHash: bot.AccessHash},
		AdminRights: BotRights(),
	}); err != nil {
		return fmt.Errorf(i18n.T("err.promote_bot")+": %w", err)
	}
	return nil
}

// resolveChannelAccess 用频道号换 access_hash。
//
// 输入：ctx；api；channelID
// 输出：access_hash；频道不在返回列表里时报错
// 预期：只读一次 channels.getChannels，供没缓存 access_hash 的调用方补权
func resolveChannelAccess(ctx context.Context, api GrantAPI, channelID int64) (int64, error) {
	res, err := api.ChannelsGetChannels(ctx, []tg.InputChannelClass{
		&tg.InputChannel{ChannelID: channelID},
	})
	if err != nil {
		return 0, fmt.Errorf(i18n.T("err.promote_bot")+": %w", err)
	}
	for _, c := range ChatsOf(res) {
		if ch, ok := c.(*tg.Channel); ok && ch.ID == channelID {
			return ch.AccessHash, nil
		}
	}
	return 0, fmt.Errorf("%s", i18n.T("err.promote_bot"))
}
