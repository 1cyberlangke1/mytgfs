package channel

// 改频道标题。
//
// Bot 账号改标题会 CHAT_ADMIN_REQUIRED（Telegram 只给 user 会话这个权限），
// 所以调用方通常传 user 会话的 api。这里只做参数校验与 access_hash 解析。

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"mytgfs/internal/i18n"
)

// IsAdminRequired 判断错误是否表示当前会话缺少频道管理权限。
//
// 输入：任意 error（可带包裹）
// 输出：是 CHAT_ADMIN_REQUIRED 时为 true
// 预期：调用方据此决定是否补授权后重试
func IsAdminRequired(err error) bool {
	e, ok := errors.AsType[*tgerr.Error](err)
	return ok && e.Type == "CHAT_ADMIN_REQUIRED"
}

// TitleAPI 是改标题需要的最小 API 面。
type TitleAPI interface {
	ChannelsGetChannels(ctx context.Context, req []tg.InputChannelClass) (tg.MessagesChatsClass, error)
	ChannelsEditTitle(ctx context.Context, req *tg.ChannelsEditTitleRequest) (tg.UpdatesClass, error)
}

// SetTitle 改频道标题。
//
// 输入：api 带管理权限的会话；channelID 频道号；accessHash 为 0 时先解析；title 新标题
// 输出：error；标题为空或上游拒绝时返回翻译后的错误
// 预期：只改标题，不动头像、目录节点与成员权限
func SetTitle(ctx context.Context, api TitleAPI, channelID, accessHash int64, title string) error {
	title = strings.TrimSpace(title)
	if title == "" {
		return fmt.Errorf("%s", i18n.T("err.title_empty"))
	}
	if accessHash == 0 {
		resolved, err := resolveTitleAccessHash(ctx, api, channelID)
		if err != nil {
			return err
		}
		accessHash = resolved
	}
	if _, err := api.ChannelsEditTitle(ctx, &tg.ChannelsEditTitleRequest{
		Channel: &tg.InputChannel{ChannelID: channelID, AccessHash: accessHash},
		Title:   title,
	}); err != nil {
		// 标题没变时 Telegram 返回 CHAT_NOT_MODIFIED，对调用方来说是成功
		if tgerr.Is(err, "CHAT_NOT_MODIFIED") {
			return nil
		}
		return fmt.Errorf("%s: %w", i18n.T("err.title_set"), err)
	}
	return nil
}

// resolveTitleAccessHash 用频道号换 access_hash。
//
// 输入：api；channelID
// 输出：access_hash；频道不在列表里时返回错误
// 预期：只读一次 channels.getChannels
func resolveTitleAccessHash(ctx context.Context, api TitleAPI, channelID int64) (int64, error) {
	res, err := api.ChannelsGetChannels(ctx, []tg.InputChannelClass{
		&tg.InputChannel{ChannelID: channelID},
	})
	if err != nil {
		return 0, fmt.Errorf("%s: %w", i18n.T("err.title_set"), err)
	}
	var chats []tg.ChatClass
	switch m := res.(type) {
	case *tg.MessagesChats:
		chats = m.Chats
	case *tg.MessagesChatsSlice:
		chats = m.Chats
	}
	for _, c := range chats {
		if ch, ok := c.(*tg.Channel); ok && ch.ID == channelID {
			return ch.AccessHash, nil
		}
	}
	return 0, fmt.Errorf("%s", i18n.T("err.title_set"))
}
