package channel

// 用 user 会话列出频道。

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

// List 列出当前 user 的所有频道/超级群。
//
// 输入：ctx；client 已登录的 user 客户端
// 输出：频道列表
// 预期：只返回 channel/supergroup，不含普通聊天
func List(ctx context.Context, client *telegram.Client) ([]Item, error) {
	res, err := client.API().MessagesGetDialogs(ctx, &tg.MessagesGetDialogsRequest{
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
