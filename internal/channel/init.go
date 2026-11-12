package channel

// 初始化：建频道并可选把 bot 设为管理员。

import (
	"context"
	"fmt"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
)

// Result 是频道初始化结果。
type Result struct {
	ChannelID  int64
	AccessHash int64
	Title      string
	BotID      int64
}

// Init 创建私有频道并把 bot 设为管理员。
//
// 输入：ctx；client user 客户端；botUsername 不含 @；title 频道名
// 输出：Result
// 预期：等价于 Create + AddBot
func Init(ctx context.Context, client *telegram.Client, botUsername, title string) (*Result, error) {
	res, err := Create(ctx, client, title)
	if err != nil {
		return nil, err
	}
	botID, err := AddBot(ctx, client, res, botUsername)
	if err != nil {
		return nil, err
	}
	res.BotID = botID
	return res, nil
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
