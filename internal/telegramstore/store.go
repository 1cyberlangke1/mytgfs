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

	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/tg"
	"golang.org/x/time/rate"

	"mytgfs/internal/i18n"
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
	ChannelsGetFullChannel(ctx context.Context, channel tg.InputChannelClass) (*tg.MessagesChatFull, error)
	MessagesSendMedia(ctx context.Context, request *tg.MessagesSendMediaRequest) (tg.UpdatesClass, error)
	MessagesSendMessage(ctx context.Context, request *tg.MessagesSendMessageRequest) (tg.UpdatesClass, error)
	MessagesEditMessage(ctx context.Context, request *tg.MessagesEditMessageRequest) (tg.UpdatesClass, error)
	MessagesUpdatePinnedMessage(ctx context.Context, request *tg.MessagesUpdatePinnedMessageRequest) (tg.UpdatesClass, error)
	ChannelsDeleteMessages(ctx context.Context, request *tg.ChannelsDeleteMessagesRequest) (*tg.MessagesAffectedMessages, error)

	// 以下由 gotd 的 downloader/uploader 直接调用，Store 自己不手写分块与并发
	UploadGetFile(ctx context.Context, request *tg.UploadGetFileRequest) (tg.UploadFileClass, error)
	UploadGetFileHashes(ctx context.Context, request *tg.UploadGetFileHashesRequest) ([]tg.FileHash, error)
	UploadReuploadCDNFile(ctx context.Context, request *tg.UploadReuploadCDNFileRequest) ([]tg.FileHash, error)
	UploadGetCDNFileHashes(ctx context.Context, request *tg.UploadGetCDNFileHashesRequest) ([]tg.FileHash, error)
	UploadGetWebFile(ctx context.Context, request *tg.UploadGetWebFileRequest) (*tg.UploadWebFile, error)
	UploadSaveFilePart(ctx context.Context, request *tg.UploadSaveFilePartRequest) (bool, error)
	UploadSaveBigFilePart(ctx context.Context, request *tg.UploadSaveBigFilePartRequest) (bool, error)
}

// Store 把文件读写到指定频道。
type Store struct {
	api       API
	channelID int64

	mu      sync.Mutex
	channel *tg.Channel

	sendMu sync.Mutex
	// limiter 是发消息节流器，x/time/rate 实现，默认每秒 1 条
	limiter *rate.Limiter

	dl          *downloader.Downloader // 由 gotd 负责分块、并发、CDN、file_reference
	threadCount int                    // 下载工作协程上限，0 用默认值

	// resumeStore 保存上传断点，nil 表示不做断点续传
	resumeStore ResumeStore
	// uploadWorkers 是上传并发分片数，0 用默认值
	uploadWorkers int
}

// New 创建存储。
func New(api API, channelID int64) *Store {
	return &Store{
		api:       api,
		channelID: channelID,
		limiter:   rate.NewLimiter(rate.Every(time.Second), 1),
		dl:        newDownloader(),
	}
}

// SetInterval 调整发送间隔，仅供测试使用。
//
// 输入：d 最小间隔；<=0 表示不限制
// 输出：无
// 预期：替换内部限流器，已有的调用不受影响
func (s *Store) SetInterval(d time.Duration) {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	if d <= 0 {
		s.limiter = rate.NewLimiter(rate.Inf, 1)
		return
	}
	s.limiter = rate.NewLimiter(rate.Every(d), 1)
}

// SetParallel 设置下载工作协程上限。
//
// 输入：n 协程数；<=0 用默认值，>32 按 32 处理
// 输出：无
// 预期：只影响下载；实际在飞请求数仍由 downloader 的自适应窗口限制
func (s *Store) SetParallel(n int) {
	switch {
	case n <= 0:
		s.threadCount = defaultThreads
	case n > 32:
		s.threadCount = 32
	default:
		s.threadCount = n
	}
}

// throttle 等待限流器放行，默认每秒最多发一条消息，降低触发 FLOOD_WAIT 的概率。
//
// 输入：ctx 用于取消等待
// 输出：ctx 错误或 nil
// 预期：并发调用按限流器排队，不会同时发出
func (s *Store) throttle(ctx context.Context) error {
	s.sendMu.Lock()
	l := s.limiter
	s.sendMu.Unlock()
	return l.Wait(ctx)
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
		return nil, DescribeError(err)
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
	// 频道不存在、或被删后重建了同 ID 之外的新频道时都会走到这里
	return nil, fmt.Errorf("%s", i18n.T("err.channel_not_visible", s.channelID))
}
