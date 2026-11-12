package telegramstore

// 把文件读写到指定 Telegram 频道。
//
// 崩溃边界：
//   - 上传在 sendMedia 之前中断：服务端丢弃临时文件，无副作用
//   - 上传在 sendMedia 之后中断：可能留下孤儿消息，需对账清理
//   - 下载/删除本身无状态，重试即可

import (
	"context"
	"fmt"
	"sync"

	"github.com/gotd/td/tg"
)

// MaxFileSize 是 Telegram 单条 document 消息的字节上限。
// 4000 parts × 512 KiB，实测 upload_max_fileparts_default=4000。
const MaxFileSize int64 = 2_097_152_000

// Part 描述一条已存储的 document 消息。
type Part struct {
	MessageID     int
	DocumentID    int64
	AccessHash    int64
	FileReference []byte
	Size          int64
}

// Store 把文件读写到指定频道。
type Store struct {
	api       *tg.Client
	channelID int64

	mu      sync.Mutex
	channel *tg.Channel
}

// New 创建存储。
func New(api *tg.Client, channelID int64) *Store {
	return &Store{api: api, channelID: channelID}
}

// ChannelID 返回目标频道 ID。
func (s *Store) ChannelID() int64 { return s.channelID }

// resolve 返回 bot 视角的频道，并缓存 access_hash。
//
// 注意：access_hash 与登录 session 绑定，换 session 后需要重新解析。
func (s *Store) resolve(ctx context.Context) (*tg.Channel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.channel != nil {
		return s.channel, nil
	}
	res, err := s.api.ChannelsGetChannels(ctx, []tg.InputChannelClass{
		&tg.InputChannel{ChannelID: s.channelID, AccessHash: 0},
	})
	if err != nil {
		return nil, fmt.Errorf("getChannels: %w", err)
	}
	var chats []tg.ChatClass
	switch m := res.(type) {
	case *tg.MessagesChats:
		chats = m.Chats
	case *tg.MessagesChatsSlice:
		chats = m.Chats
	}
	for _, c := range chats {
		if ch, ok := c.(*tg.Channel); ok {
			s.channel = ch
			return ch, nil
		}
	}
	return nil, fmt.Errorf("channel %d not found", s.channelID)
}
