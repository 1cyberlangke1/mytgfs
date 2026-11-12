package telegramstore

import (
	"context"
	"fmt"

	"github.com/gotd/td/tg"
)

// Rename 修改消息的 caption（路径）。
//
// 输入：ctx；messageID；caption 新的 caption
// 输出：error
// 预期：只改 caption，不重传文件字节
func (s *Store) Rename(ctx context.Context, messageID int, caption string) error {
	ch, err := s.resolve(ctx)
	if err != nil {
		return err
	}
	req := &tg.MessagesEditMessageRequest{
		Peer: &tg.InputPeerChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash},
		ID:   messageID,
	}
	// Message 是 flags.14?string，必须用 SetMessage 才会设置 flags。
	req.SetMessage(caption)
	if err := s.throttle(ctx); err != nil {
		return err
	}
	if _, err := s.api.MessagesEditMessage(ctx, req); err != nil {
		return DescribeError(fmt.Errorf("editMessage: %w", s.noteErr(err)))
	}
	return nil
}
