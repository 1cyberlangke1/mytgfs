package sync

import (
	"context"
	"encoding/json"
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

type refCaption struct {
	Ref int `json:"ref"`
}

// parseRef 解析文件 caption 里的元信息消息引用。
func parseRef(s string) (int, bool) {
	var r refCaption
	if err := json.Unmarshal([]byte(s), &r); err != nil || r.Ref == 0 {
		return 0, false
	}
	return r.Ref, true
}

// fetchMeta 按消息 ID 拉取完整元信息文本。
func (f TelegramFetcher) fetchMeta(ctx context.Context, ref int) (string, error) {
	res, err := f.API.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{
		Channel: &tg.InputChannel{ChannelID: f.ChannelID, AccessHash: f.AccessHash},
		ID:      []tg.InputMessageClass{&tg.InputMessageID{ID: ref}},
	})
	if err != nil {
		return "", err
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
	for _, mc := range msgs {
		if msg, ok := mc.(*tg.Message); ok {
			return msg.Message, nil
		}
	}
	return "", fmt.Errorf("元信息消息 %d 不存在", ref)
}

// History 拉取最近 limit 条消息。
//
// 输入：limit 条数
// 输出：只含 document 消息；caption 为 {"ref":id} 时自动拉取完整元信息
// 预期：非 document 消息被跳过
func (f TelegramFetcher) History(ctx context.Context, limit int) ([]Message, error) {
	return f.history(ctx, 0, limit)
}

// HistorySince 增量拉取 ID 大于 minID 的消息。
//
// 输入：minID 已同步的最大消息 ID；limit 条数
// 输出：同 History
// 预期：minID 为 0 时退化为拉最近 limit 条
func (f TelegramFetcher) HistorySince(ctx context.Context, minID int, limit int) ([]Message, error) {
	return f.history(ctx, minID, limit)
}

func (f TelegramFetcher) history(ctx context.Context, minID, limit int) ([]Message, error) {
	req := &tg.MessagesGetHistoryRequest{
		Peer:  &tg.InputPeerChannel{ChannelID: f.ChannelID, AccessHash: f.AccessHash},
		Limit: limit,
	}
	if minID > 0 {
		req.MinID = minID
	}
	res, err := f.API.MessagesGetHistory(ctx, req)
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

		caption := msg.Message
		if ref, ok := parseRef(caption); ok {
			if full, err := f.fetchMeta(ctx, ref); err == nil {
				caption = full
			}
		}

		out = append(out, Message{
			ID:            msg.ID,
			Caption:       caption,
			DocumentID:    doc.ID,
			AccessHash:    doc.AccessHash,
			FileReference: doc.FileReference,
			Size:          doc.Size,
			Date:          time.Unix(int64(msg.Date), 0),
		})
	}
	return out, nil
}
