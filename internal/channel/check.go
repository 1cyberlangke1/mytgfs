package channel

// 频道健康检查：确认 bot 还在频道里、还有发言权限。
//
// 频道随时可能被主人手动删掉、把 bot 踢出去、或者取消 bot 的管理员权限，
// 这些情况下本地配置和索引都还是旧的，只有真正访问 Telegram 才会暴露。
// volume check 用只读 RPC 做一次体检，不往频道里发任何消息。

import (
	"context"
	"fmt"

	"github.com/gotd/td/tg"

	"mytgfs/internal/i18n"
)

// CheckAPI 是体检需要的最小接口。
type CheckAPI interface {
	ChannelsGetChannels(ctx context.Context, req []tg.InputChannelClass) (tg.MessagesChatsClass, error)
	ChannelsGetParticipant(ctx context.Context, req *tg.ChannelsGetParticipantRequest) (*tg.ChannelsChannelParticipant, error)
}

// Health 是一个卷的健康状态。
type Health struct {
	ChannelID int64
	Title     string
	// CanPost 表示 bot 能往频道发消息（频道只有管理员能发）
	CanPost bool
}

// Check 检查 bot 对频道的可见性与发言权限。
//
// 输入：ctx；api bot 会话；channelID 频道 id
// 输出：Health；频道不可见或无发言权限时返回 error
// 预期：只用 channels.getChannels + channels.getParticipant，不写频道
func Check(ctx context.Context, api CheckAPI, channelID int64) (*Health, error) {
	res, err := api.ChannelsGetChannels(ctx, []tg.InputChannelClass{
		&tg.InputChannel{ChannelID: channelID, AccessHash: 0},
	})
	if err != nil {
		return nil, fmt.Errorf("getChannels: %w", err)
	}
	var ch *tg.Channel
	var chats []tg.ChatClass
	switch m := res.(type) {
	case *tg.MessagesChats:
		chats = m.Chats
	case *tg.MessagesChatsSlice:
		chats = m.Chats
	}
	for _, c := range chats {
		if v, ok := c.(*tg.Channel); ok && v.ID == channelID {
			ch = v
			break
		}
	}
	if ch == nil {
		return nil, fmt.Errorf("%s", i18n.T("err.channel_not_visible", channelID))
	}

	me, err := api.ChannelsGetParticipant(ctx, &tg.ChannelsGetParticipantRequest{
		Channel:     &tg.InputChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash},
		Participant: &tg.InputPeerSelf{},
	})
	if err != nil {
		return nil, fmt.Errorf("getParticipant: %w", err)
	}
	h := &Health{ChannelID: ch.ID, Title: ch.Title}
	if me == nil || me.Participant == nil {
		return h, fmt.Errorf("%s", i18n.T("err.channel_check_unknown"))
	}
	switch p := me.Participant.(type) {
	case *tg.ChannelParticipantCreator:
		// 创建者默认拥有全部权限
		h.CanPost = true
	case *tg.ChannelParticipantAdmin:
		h.CanPost = p.AdminRights.PostMessages
	}
	if !h.CanPost {
		return h, fmt.Errorf("%s", i18n.T("err.channel_check_no_post"))
	}
	return h, nil
}
