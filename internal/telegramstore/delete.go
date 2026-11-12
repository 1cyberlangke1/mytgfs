package telegramstore

import (
	"context"
	"fmt"

	"github.com/gotd/td/tg"
)

// Delete 删除指定消息，幂等。
//
// 输入：ids 消息 ID
// 输出：error
// 预期：重复删除不报错
func (s *Store) Delete(ctx context.Context, ids ...int) error {
	if len(ids) == 0 {
		return nil
	}
	ch, err := s.resolve(ctx)
	if err != nil {
		return err
	}
	_, err = s.api.ChannelsDeleteMessages(ctx, &tg.ChannelsDeleteMessagesRequest{
		Channel: &tg.InputChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash},
		ID:      ids,
	})
	if err != nil {
		return fmt.Errorf("deleteMessages: %w", err)
	}
	return nil
}
