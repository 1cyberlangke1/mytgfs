package telegramstore

// file_reference 会过期：索引里存的是上传时的引用，隔一段时间读取会被
// Telegram 以 FILE_REFERENCE_EXPIRED / FILE_REFERENCE_INVALID 拒绝。
// 这里按消息 ID 重新拉一次 document 元数据，让调用方更新索引后重试。

import (
	"context"
	"fmt"

	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/tg"
)

// IsFileReferenceError 判断错误是否为 file_reference 失效。
//
// 输入：任意 error
// 输出：true 表示可以按消息 ID 刷新元数据后重试
// 预期：直接交给 gotd downloader 判定，不自己匹配错误字符串
func IsFileReferenceError(err error) bool {
	return downloader.IsFileReferenceError(err)
}

// Refresh 按消息 ID 重新拉取 document 元数据。
//
// 输入：messageID 频道里的文件消息 ID
// 输出：最新的 Part（含有效 file_reference）
// 预期：消息不存在或不是 document 时返回错误，不修改任何本地状态
func (s *Store) Refresh(ctx context.Context, messageID int) (*Part, error) {
	ch, err := s.resolve(ctx)
	if err != nil {
		return nil, err
	}
	res, err := s.api.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{
		Channel: &tg.InputChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash},
		ID:      []tg.InputMessageClass{&tg.InputMessageID{ID: messageID}},
	})
	if err != nil {
		return nil, DescribeError(fmt.Errorf("getMessages: %w", s.noteErr(err)))
	}
	for _, mc := range messagesOf(res) {
		msg, ok := mc.(*tg.Message)
		if !ok {
			continue
		}
		doc, ok := documentOf(msg.Media)
		if !ok {
			continue
		}
		return &Part{
			MessageID:     msg.ID,
			DocumentID:    doc.ID,
			AccessHash:    doc.AccessHash,
			FileReference: doc.FileReference,
			Size:          doc.Size,
		}, nil
	}
	return nil, ErrNoDocument
}

// messagesOf 把 MessagesClass 的三种形态摊平成消息列表。
//
// 输入：ChannelsGetMessages 的返回
// 输出：消息列表；未知形态返回 nil
// 预期：只做类型分派，不改变顺序
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
