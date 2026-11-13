package telegramstore

// 节点消息的远端读写。
//
// 每条消息自述名字与父消息号，改名/移动只编辑这一条消息。
// 名字限长后 body 恒小于 caption 上限，正文直接写在消息里，不拆替身消息。

import (
	"context"
	"math/rand"

	"github.com/gotd/td/tg"
)

// SendText 发一条纯文本消息并返回消息号。
//
// 输入：ctx；text 正文
// 输出：消息号；error
// 预期：走与文件消息相同的发送节流，避免触发频道限速
func (s *Store) SendText(ctx context.Context, text string) (int, error) {
	ch, err := s.resolve(ctx)
	if err != nil {
		return 0, err
	}
	if err := s.throttle(ctx); err != nil {
		return 0, err
	}
	updates, err := s.api.MessagesSendMessage(ctx, &tg.MessagesSendMessageRequest{
		Peer:     &tg.InputPeerChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash},
		Message:  text,
		RandomID: rand.Int63(),
	})
	if err != nil {
		return 0, DescribeError(s.noteErr(err))
	}
	return extractMessageID(updates)
}

// EditText 改写一条文本消息的正文。
//
// 输入：ctx；id 消息号；text 新正文
// 输出：error
// 预期：只改正文，不动其他属性；消息已不存在时返回错误
func (s *Store) EditText(ctx context.Context, id int, text string) error {
	ch, err := s.resolve(ctx)
	if err != nil {
		return err
	}
	req := &tg.MessagesEditMessageRequest{
		Peer: &tg.InputPeerChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash},
		ID:   id,
	}
	req.SetMessage(text)
	if err := s.throttle(ctx); err != nil {
		return err
	}
	if _, err := s.api.MessagesEditMessage(ctx, req); err != nil {
		return DescribeError(s.noteErr(err))
	}
	return nil
}

// Text 读取一条消息的正文。
//
// 输入：ctx；id 消息号
// 输出：正文；error
// 预期：消息不存在返回 ErrNoMessage
func (s *Store) Text(ctx context.Context, id int) (string, error) {
	ch, err := s.resolve(ctx)
	if err != nil {
		return "", err
	}
	res, err := s.api.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{
		Channel: &tg.InputChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash},
		ID:      []tg.InputMessageClass{&tg.InputMessageID{ID: id}},
	})
	if err != nil {
		return "", DescribeError(s.noteErr(err))
	}
	for _, mc := range messagesOf(res) {
		if msg, ok := mc.(*tg.Message); ok {
			return msg.Message, nil
		}
	}
	return "", ErrNoMessage
}

// SendNode 发一条节点消息，返回消息号。
//
// 输入：ctx；body 已编码的节点正文
// 输出：消息号；error
// 预期：正文直接写在消息里
func (s *Store) SendNode(ctx context.Context, body string) (int, error) {
	return s.SendText(ctx, body)
}

// EditNode 改写一条节点消息的正文。
//
// 输入：ctx；id 消息号；body 新正文
// 输出：error
// 预期：只改正文；消息已不存在时返回错误
func (s *Store) EditNode(ctx context.Context, id int, body string) error {
	return s.EditText(ctx, id, body)
}

// ReadNode 读取节点消息正文。
//
// 输入：ctx；id 消息号
// 输出：正文；error
// 预期：消息不存在返回 ErrNoMessage，供改名/移动失败后按远端对账
func (s *Store) ReadNode(ctx context.Context, id int) (string, error) {
	return s.Text(ctx, id)
}

// extractMessageID 从 Updates 里提取消息 ID。
//
// 输入：MessagesSendMessage 的返回
// 输出：消息 ID；error
// 预期：兼容普通消息、频道消息、短消息三种形态
func extractMessageID(u tg.UpdatesClass) (int, error) {
	switch upd := u.(type) {
	case *tg.Updates:
		for _, item := range upd.Updates {
			switch x := item.(type) {
			case *tg.UpdateNewMessage:
				if msg, ok := x.Message.(*tg.Message); ok {
					return msg.ID, nil
				}
			case *tg.UpdateNewChannelMessage:
				if msg, ok := x.Message.(*tg.Message); ok {
					return msg.ID, nil
				}
			}
		}
	case *tg.UpdateShortSentMessage:
		return upd.ID, nil
	}
	return 0, ErrNoMessage
}
