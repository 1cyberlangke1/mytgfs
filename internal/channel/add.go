package channel

// 把 bot 加入已有频道。

import (
	"context"
	"fmt"

	"github.com/gotd/td/telegram"
)

// Add 把 bot 设为已有频道的管理员。
//
// 输入：ctx；client user 客户端；channelID 目标频道；botUsername 不含 @
// 输出：Result
// 预期：要求当前 user 是该频道管理员
func Add(ctx context.Context, client *telegram.Client, channelID int64, botUsername string) (*Result, error) {
	items, err := List(ctx, client)
	if err != nil {
		return nil, err
	}
	var target *Item
	for i := range items {
		if items[i].ID == channelID {
			target = &items[i]
			break
		}
	}
	if target == nil {
		return nil, fmt.Errorf("当前 user 的频道列表里没有 %d", channelID)
	}

	res := &Result{
		ChannelID:  target.ID,
		AccessHash: target.AccessHash,
		Title:      target.Title,
	}
	botID, err := AddBot(ctx, client, res, botUsername)
	if err != nil {
		return nil, err
	}
	res.BotID = botID
	return res, nil
}
