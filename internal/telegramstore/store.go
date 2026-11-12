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
	"time"

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

// API 是 Store 用到的 Telegram RPC 子集。
//
// 输入：各 RPC 参数；输出：Telegram 原始返回
// 预期：只声明实际使用的方法，测试可注入假实现，不需要真实网络
type API interface {
	ChannelsGetChannels(ctx context.Context, id []tg.InputChannelClass) (tg.MessagesChatsClass, error)
	ChannelsGetMessages(ctx context.Context, request *tg.ChannelsGetMessagesRequest) (tg.MessagesMessagesClass, error)
	MessagesSendMedia(ctx context.Context, request *tg.MessagesSendMediaRequest) (tg.UpdatesClass, error)
	MessagesSendMessage(ctx context.Context, request *tg.MessagesSendMessageRequest) (tg.UpdatesClass, error)
	MessagesEditMessage(ctx context.Context, request *tg.MessagesEditMessageRequest) (tg.UpdatesClass, error)
	ChannelsDeleteMessages(ctx context.Context, request *tg.ChannelsDeleteMessagesRequest) (*tg.MessagesAffectedMessages, error)
	UploadGetFile(ctx context.Context, request *tg.UploadGetFileRequest) (tg.UploadFileClass, error)
	UploadSaveFilePart(ctx context.Context, request *tg.UploadSaveFilePartRequest) (bool, error)
	UploadSaveBigFilePart(ctx context.Context, request *tg.UploadSaveBigFilePartRequest) (bool, error)
}

// Store 把文件读写到指定频道。
type Store struct {
	api       API
	channelID int64

	mu      sync.Mutex
	channel *tg.Channel

	sendMu   sync.Mutex
	lastSend time.Time
	interval time.Duration // 发消息最小间隔，测试可调小
}

// New 创建存储。
func New(api API, channelID int64) *Store {
	return &Store{api: api, channelID: channelID, interval: time.Second}
}

// SetInterval 调整发送间隔，仅供测试使用。
func (s *Store) SetInterval(d time.Duration) { s.interval = d }

// throttle 串行化发消息操作，默认最小间隔 1 秒，降低触发 FLOOD_WAIT 的概率。
func (s *Store) throttle(ctx context.Context) error {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	if s.interval > 0 {
		if wait := time.Until(s.lastSend.Add(s.interval)); wait > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(wait):
			}
		}
	}
	s.lastSend = time.Now()
	return nil
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
