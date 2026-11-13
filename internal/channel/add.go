package channel

// 把 bot 加入已有频道。

import (
	"context"
	"fmt"

	"mytgfs/internal/i18n"
)

// Add 把 bot 设为已有频道的管理员。
//
// 输入：ctx；api user 会话的 Telegram API；channelID 目标频道；botUsername 不含 @
// 输出：Result
// 预期：要求当前 user 是该频道管理员
func Add(ctx context.Context, api API, channelID int64, botUsername string) (*Result, error) {
	items, err := List(ctx, api)
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
		return nil, fmt.Errorf(i18n.T("err.channel_not_in_list"), channelID)
	}

	res := &Result{
		ChannelID:  target.ID,
		AccessHash: target.AccessHash,
		Title:      target.Title,
	}
	botID, err := AddBot(ctx, api, res, botUsername)
	if err != nil {
		return nil, err
	}
	res.BotID = botID
	return res, nil
}
