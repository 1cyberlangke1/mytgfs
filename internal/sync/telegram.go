package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/gotd/td/tg"

	"mytgfs/internal/i18n"
)

// metaFetcher 是读取元信息消息所需的最小能力。
//
// 输入：channels.getMessages 参数
// 输出：消息列表
// 预期：用户会话与 bot 会话都满足，测试可注入假实现
type metaFetcher interface {
	ChannelsGetMessages(ctx context.Context, request *tg.ChannelsGetMessagesRequest) (tg.MessagesMessagesClass, error)
}

// HistoryAPI 是历史回填所需的最小 RPC 子集。
//
// 输入：messages.getHistory / channels.getMessages 参数
// 输出：消息列表
// 预期：只声明用到的两个方法，单测可 mock，不需要真实网络
type HistoryAPI interface {
	MessagesGetHistory(ctx context.Context, request *tg.MessagesGetHistoryRequest) (tg.MessagesMessagesClass, error)
	ChannelsGetMessages(ctx context.Context, request *tg.ChannelsGetMessagesRequest) (tg.MessagesMessagesClass, error)
}

// TelegramFetcher 用 user 会话拉频道历史。
//
// 注意：messages.getHistory 是 user-only，bot 调用会 BOT_METHOD_INVALID。
type TelegramFetcher struct {
	API        HistoryAPI
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

// fetchMetaText 按消息 ID 拉取完整元信息文本。
//
// 输入：api 客户端、频道 ID 与 access hash、ref 元信息消息 ID
// 输出：消息正文、error
// 预期：channels.getMessages 对 bot/user 都可用；消息不存在时报错
func fetchMetaText(ctx context.Context, api metaFetcher, channelID, accessHash int64, ref int) (string, error) {
	res, err := api.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{
		Channel: &tg.InputChannel{ChannelID: channelID, AccessHash: accessHash},
		ID:      []tg.InputMessageClass{&tg.InputMessageID{ID: ref}},
	})
	if err != nil {
		return "", err
	}
	for _, mc := range messagesOf(res) {
		if msg, ok := mc.(*tg.Message); ok {
			return msg.Message, nil
		}
	}
	return "", fmt.Errorf(i18n.T("err.meta_msg_missing"), ref)
}

// messagesOf 把 MessagesMessagesClass 的三种形态摊平成消息列表。
func messagesOf(res tg.MessagesMessagesClass) []tg.MessageClass {
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

// messageFromTG 把 TL 消息转成本包的 Message。
//
// 输入：ctx、api、频道 ID 与 access hash、TL 消息
// 输出：Message、是否是需要同步的文件消息、error
// 预期：非 document 消息返回 ok=false；caption 是 {"ref":id} 时回查完整元信息
func messageFromTG(ctx context.Context, api metaFetcher, channelID, accessHash int64, msg *tg.Message) (Message, bool, error) {
	media, ok := msg.Media.(*tg.MessageMediaDocument)
	if !ok {
		return Message{}, false, nil
	}
	doc, ok := media.Document.(*tg.Document)
	if !ok {
		return Message{}, false, nil
	}

	caption := msg.Message
	if ref, ok := parseRef(caption); ok {
		if full, err := fetchMetaText(ctx, api, channelID, accessHash, ref); err == nil {
			caption = full
		}
	}
	return Message{
		ID:            msg.ID,
		Caption:       caption,
		DocumentID:    doc.ID,
		AccessHash:    doc.AccessHash,
		FileReference: doc.FileReference,
		Size:          doc.Size,
		Date:          time.Unix(int64(msg.Date), 0),
	}, true, nil
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
		return nil, err
	}
	msgs := messagesOf(res)
	out := make([]Message, 0, len(msgs))
	for _, mc := range msgs {
		msg, ok := mc.(*tg.Message)
		if !ok {
			continue
		}
		m, isFile, err := messageFromTG(ctx, f.API, f.ChannelID, f.AccessHash, msg)
		if err != nil {
			return nil, err
		}
		if !isFile {
			continue
		}
		out = append(out, m)
	}
	return out, nil
}

// HistoryBefore 拉取比 offsetID 更早的一页，用于倒序重建索引。
//
// 输入：offsetID 上一页最小的消息 ID（0 表示从最新开始）；limit 原始条数
// 输出：文件消息；下一页游标（本页最小消息 ID，0 表示已到底）
// 预期：游标按原始消息计算，非文件消息也参与，避免整页都是文本时提前停止
func (f TelegramFetcher) HistoryBefore(ctx context.Context, offsetID, limit int) ([]Message, int, error) {
	req := &tg.MessagesGetHistoryRequest{
		Peer:  &tg.InputPeerChannel{ChannelID: f.ChannelID, AccessHash: f.AccessHash},
		Limit: limit,
	}
	if offsetID > 0 {
		req.OffsetID = offsetID
	}
	res, err := f.API.MessagesGetHistory(ctx, req)
	if err != nil {
		return nil, 0, err
	}
	raw := messagesOf(res)
	next := 0
	out := make([]Message, 0, len(raw))
	for _, mc := range raw {
		msg, ok := mc.(*tg.Message)
		if !ok {
			continue
		}
		if next == 0 || msg.ID < next {
			next = msg.ID
		}
		m, isFile, err := messageFromTG(ctx, f.API, f.ChannelID, f.AccessHash, msg)
		if err != nil {
			return nil, 0, err
		}
		if isFile {
			out = append(out, m)
		}
	}
	return out, next, nil
}
