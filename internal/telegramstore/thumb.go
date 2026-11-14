package telegramstore

// 视频缩略图：Telegram 为上传的视频生成了缩略图，直接取最大一档，
// 不再本地解码视频帧（不依赖 ffmpeg）。

import (
	"context"
	"fmt"

	"github.com/gotd/td/tg"

	"mytgfs/internal/i18n"
)

// Thumb 按文件消息 ID 取 document 最大缩略图字节。
//
// 输入：ctx；messageID 频道里的文件消息 ID
// 输出：缩略图字节（通常是 JPEG）
// 预期：消息不存在、不是 document、或没有缩略图时返回错误；只读，不改索引
func (s *Store) Thumb(ctx context.Context, messageID int) ([]byte, error) {
	ch, err := s.resolve(ctx)
	if err != nil {
		return nil, err
	}
	res, err := s.api.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{
		Channel: &tg.InputChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash},
		ID:      []tg.InputMessageClass{&tg.InputMessageID{ID: messageID}},
	})
	if err != nil {
		return nil, DescribeError(s.noteErr(err))
	}
	var doc *tg.Document
	for _, mc := range messagesOf(res) {
		msg, ok := mc.(*tg.Message)
		if !ok {
			continue
		}
		if d, ok := documentOf(msg.Media); ok {
			doc = d
			break
		}
	}
	if doc == nil {
		return nil, ErrNoDocument
	}

	size, ok := largestThumb(doc.Thumbs)
	if !ok {
		return nil, fmt.Errorf("%s", i18n.T("err.thumb_missing"))
	}
	loc := &tg.InputDocumentFileLocation{
		ID:            doc.ID,
		AccessHash:    doc.AccessHash,
		FileReference: doc.FileReference,
		ThumbSize:     size.typ,
	}
	buf := make([]byte, size.bytes)
	b := s.dl.Download(s.api, loc).
		WithSize(int64(size.bytes)).
		WithThreads(s.threads())
	if _, err := b.Parallel(ctx, &rangeWriterAt{buf: buf}); err != nil {
		return nil, DescribeError(fmt.Errorf("%s: %w", i18n.T("err.thumb_fetch"), s.noteErr(err)))
	}
	return buf, nil
}

// thumbChoice 是选中的缩略图档位。
type thumbChoice struct {
	typ   string
	bytes int
}

// largestThumb 取字节数最大的缩略图档位。
//
// 输入：document.Thumbs
// 输出：档位与是否找到
// 预期：跳过没有尺寸信息的 stripped/cached 项
func largestThumb(thumbs []tg.PhotoSizeClass) (thumbChoice, bool) {
	var best thumbChoice
	found := false
	for _, t := range thumbs {
		c := thumbChoice{}
		switch v := t.(type) {
		case *tg.PhotoSize:
			c = thumbChoice{typ: v.Type, bytes: v.Size}
		case *tg.PhotoSizeProgressive:
			if len(v.Sizes) == 0 {
				continue
			}
			c = thumbChoice{typ: v.Type, bytes: v.Sizes[len(v.Sizes)-1]}
		default:
			continue
		}
		if c.bytes <= 0 || c.typ == "" {
			continue
		}
		if !found || c.bytes > best.bytes {
			best, found = c, true
		}
	}
	return best, found
}
