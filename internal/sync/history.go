package sync

// 全量历史拉取：重建索引时一次拿回频道里的所有节点消息。
//
// messages.getHistory 是 user-only：bot 无法枚举历史，因此全量重建依赖 user 会话；
// bot 侧只按已知消息号读取（FetchNodes）。

import (
	"context"
	"fmt"

	"github.com/gotd/td/tg"

	"mytgfs/internal/channel"
	"mytgfs/internal/i18n"
	"mytgfs/internal/telegramstore"
)

// historyPageSize 是单次 messages.getHistory 拉取条数。
const historyPageSize = 100

// maxHistoryPages 限制翻页次数，防止异常数据导致死循环。
const maxHistoryPages = 100000

// HistoryAPI 是全量历史拉取需要的 RPC 子集。
type HistoryAPI interface {
	ChannelsGetChannels(ctx context.Context, id []tg.InputChannelClass) (tg.MessagesChatsClass, error)
	MessagesGetHistory(ctx context.Context, request *tg.MessagesGetHistoryRequest) (tg.MessagesMessagesClass, error)
}

// TelegramHistory 用 user 会话拉取频道全部节点消息。
type TelegramHistory struct {
	API       HistoryAPI
	ChannelID int64
}

// AllNodes 从最新一条翻到最早一条，解析出全部节点。
//
// 输入：ctx
// 输出：节点列表（顺序按历史倒序）；error
// 预期：
//   - 只收本程序格式的消息，聊天消息忽略
//   - 翻页游标不前进即停止，避免死循环
func (h *TelegramHistory) AllNodes(ctx context.Context) ([]telegramstore.NodeInfo, error) {
	ch, err := h.channel(ctx)
	if err != nil {
		return nil, err
	}
	peer := &tg.InputPeerChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash}
	// 逐页解析：只留节点结果，不再把整段历史消息对象都攒在内存里
	var out []telegramstore.NodeInfo
	offset := 0
	for range maxHistoryPages {
		res, err := h.API.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{
			Peer:     peer,
			OffsetID: offset,
			Limit:    historyPageSize,
		})
		if err != nil {
			return nil, err
		}
		page := historyMessages(res)
		if len(page) == 0 {
			return out, nil
		}
		for _, mc := range page {
			if msg, ok := mc.(*tg.Message); ok && msg.Message != "" {
				if info, ok := telegramstore.NodeOf(msg, msg.Message); ok {
					out = append(out, info)
				}
			}
		}
		last, ok := page[len(page)-1].(*tg.Message)
		if !ok || last.ID <= 0 {
			return out, nil
		}
		if offset != 0 && last.ID >= offset {
			// 游标没有前进：数据异常，直接停
			return out, nil
		}
		offset = last.ID
	}
	return nil, fmt.Errorf(i18n.T("err.history_too_long"), maxHistoryPages)
}

// channel 解析频道，拿到 user 视角的 access_hash。
func (h *TelegramHistory) channel(ctx context.Context) (*tg.Channel, error) {
	res, err := h.API.ChannelsGetChannels(ctx, []tg.InputChannelClass{
		&tg.InputChannel{ChannelID: h.ChannelID, AccessHash: 0},
	})
	if err != nil {
		return nil, err
	}
	for _, c := range channel.ChatsOf(res) {
		if ch, ok := c.(*tg.Channel); ok {
			return ch, nil
		}
	}
	return nil, ChannelUnavailableError{ChannelID: h.ChannelID}
}

// historyMessages 摊平三种返回形态。
func historyMessages(res tg.MessagesMessagesClass) []tg.MessageClass {
	switch m := res.(type) {
	case *tg.MessagesMessages:
		return m.Messages
	case *tg.MessagesMessagesSlice:
		return m.Messages
	case *tg.MessagesChannelMessages:
		return m.Messages
	}
	return nil
}
