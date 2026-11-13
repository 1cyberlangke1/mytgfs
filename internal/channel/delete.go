package channel

// 解析与删除频道。
//
// channels.deleteChannel 只有 user 会话能调（users-only），bot 调用会失败；
// 解析 access_hash 用 channels.getChannels，access_hash 传 0 也能解析出真实值。

import (
	"context"
	"errors"
	"fmt"

	"github.com/gotd/td/tg"

	"mytgfs/internal/i18n"
)

// ResolveAPI 是解析频道需要的最小接口。
type ResolveAPI interface {
	ChannelsGetChannels(ctx context.Context, req []tg.InputChannelClass) (tg.MessagesChatsClass, error)
}

// DeleteAPI 是删除频道需要的最小接口。
type DeleteAPI interface {
	ChannelsDeleteChannel(ctx context.Context, channel tg.InputChannelClass) (tg.UpdatesClass, error)
}

// Resolve 取频道的 access_hash 与标题。
//
// 输入：ctx；api user 会话；channelID 频道 id
// 输出：Result（BotID 为 0，未涉及 bot）
// 预期：access_hash 传 0 也能解析；频道不存在或无权访问时返回 error
func Resolve(ctx context.Context, api ResolveAPI, channelID int64) (*Result, error) {
	res, err := api.ChannelsGetChannels(ctx, []tg.InputChannelClass{
		&tg.InputChannel{ChannelID: channelID, AccessHash: 0},
	})
	if err != nil {
		return nil, err
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
			return &Result{ChannelID: ch.ID, AccessHash: ch.AccessHash, Title: ch.Title}, nil
		}
	}
	return nil, fmt.Errorf(i18n.T("err.channel_missing"), channelID)
}

// Delete 永久删除频道及其全部消息。
//
// 输入：ctx；api user 会话；res 已解析的频道
// 输出：error
// 预期：不可逆；只能由频道创建者或管理员调用
func Delete(ctx context.Context, api DeleteAPI, res *Result) error {
	if res == nil || res.ChannelID == 0 {
		return errors.New(i18n.T("err.channel_id_empty"))
	}
	if _, err := api.ChannelsDeleteChannel(ctx, &tg.InputChannel{
		ChannelID:  res.ChannelID,
		AccessHash: res.AccessHash,
	}); err != nil {
		return err
	}
	return nil
}
