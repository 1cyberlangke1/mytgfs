package sync

import (
	"context"
	"fmt"
	"time"

	"github.com/gotd/td/tg"
)

// TelegramFetcher 用 user 会话拉频道历史。
//
// 注意：messages.getHistory 是 user-only，bot 调用会 BOT_METHOD_INVALID。
type TelegramFetcher struct {
	API        *tg.Client
	ChannelID  int64
	AccessHash int64 // user 视角
}

// History 拉取最近 limit 条消息。
//
// 输入：limit 条数
// 输出：只含 document 消息
// 预期：非 document 消息被跳过
func (f TelegramFetcher) History(ctx context.Context, limit int) ([]Message, error) {
	res, err := f.API.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{
		Peer:  &tg.InputPeerChannel{ChannelID: f.ChannelID, AccessHash: f.AccessHash},
		Limit: limit,
	})
	if err != nil {
		return nil, fmt.Errorf("getHistory: %w", err)
	}
	var msgs []tg.MessageClass
	switch m := res.(type) {
	case *tg.MessagesMessages:
		msgs = m.Messages
	case *tg.MessagesMessagesSlice:
		msgs = m.Messages
	case *tg.MessagesChannelMessages:
		msgs = m.Messages
	}
	out := make([]Message, 0, len(msgs))
	for _, mc := range msgs {
		msg, ok := mc.(*tg.Message)
		if !ok {
			continue
		}
		media, ok := msg.Media.(*tg.MessageMediaDocument)
		if !ok {
			continue
		}
		doc, ok := media.Document.(*tg.Document)
		if !ok {
			continue
		}
		out = append(out, Message{
			ID:            msg.ID,
			Caption:       msg.Message,
			DocumentID:    doc.ID,
			AccessHash:    doc.AccessHash,
			FileReference: doc.FileReference,
			Size:          doc.Size,
			Date:          time.Unix(int64(msg.Date), 0),
		})
	}
	return out, nil
}
