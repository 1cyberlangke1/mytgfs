package channel

// 用 user 会话列出「当前 user 能管理」的频道。

import (
	"context"
	"fmt"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
)

// Item 是一个频道摘要。
type Item struct {
	ID         int64
	AccessHash int64
	Title      string
	Username   string
	Creator    bool
}

// List 列出当前 user 能管理的频道/超级群。
//
// 输入：ctx；client user 客户端；all 为 true 时额外查普通 admin
// 输出：频道列表
// 预期：默认只返回 creator（不额外发 RPC）；all 时对每个频道查 getFullChannel
func List(ctx context.Context, client *telegram.Client, all bool) ([]Item, error) {
	api := client.API()
	res, err := api.MessagesGetDialogs(ctx, &tg.MessagesGetDialogsRequest{
		OffsetPeer: &tg.InputPeerEmpty{},
		Limit:      200,
	})
	if err != nil {
		return nil, fmt.Errorf("getDialogs: %w", err)
	}

	var chats []tg.ChatClass
	switch m := res.(type) {
	case *tg.MessagesDialogs:
		chats = m.Chats
	case *tg.MessagesDialogsSlice:
		chats = m.Chats
	}

	out := make([]Item, 0, len(chats))
	for _, c := range chats {
		ch, ok := c.(*tg.Channel)
		if !ok {
			continue
		}

		manageable := ch.Creator
		if !manageable && all {
			full, err := api.ChannelsGetFullChannel(ctx, &tg.InputChannel{
				ChannelID:  ch.ID,
				AccessHash: ch.AccessHash,
			})
			if err == nil {
				if cf, ok := full.FullChat.(*tg.ChannelFull); ok {
					manageable = cf.CanDeleteChannel
				}
			}
		}
		if !manageable {
			continue
		}

		out = append(out, Item{
			ID:         ch.ID,
			AccessHash: ch.AccessHash,
			Title:      ch.Title,
			Username:   ch.Username,
			Creator:    ch.Creator,
		})
	}
	return out, nil
}
