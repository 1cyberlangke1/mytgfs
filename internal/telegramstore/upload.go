package telegramstore

import (
	"context"
	"fmt"
	"io"
	"math/rand"

	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"
)

// Upload 上传文件并发送到频道。
//
// 输入：r 数据源；size 字节数；name 文件名；mime MIME；caption 说明文字
// 输出：Part
// 预期：
//   - size > MaxFileSize 立即返回 ErrTooLarge
//   - 崩溃在 sendMedia 之前：服务端丢弃临时文件，无副作用
//   - 崩溃在 sendMedia 之后：可能留下孤儿消息，需要外部对账
func (s *Store) Upload(ctx context.Context, r io.Reader, size int64, name, mime, caption string) (*Part, error) {
	if size < 0 || size > MaxFileSize {
		return nil, ErrTooLarge
	}
	ch, err := s.resolve(ctx)
	if err != nil {
		return nil, err
	}

	up := uploader.NewUploader(s.api).WithPartSize(512 * 1024)
	inputFile, err := up.Upload(ctx, uploader.NewUpload(name, r, size))
	if err != nil {
		return nil, fmt.Errorf("upload: %w", err)
	}

	updates, err := s.api.MessagesSendMedia(ctx, &tg.MessagesSendMediaRequest{
		Peer: &tg.InputPeerChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash},
		Media: &tg.InputMediaUploadedDocument{
			File:     inputFile,
			MimeType: mime,
			Attributes: []tg.DocumentAttributeClass{
				&tg.DocumentAttributeFilename{FileName: name},
			},
		},
		Message:  caption,
		RandomID: rand.Int63(),
	})
	if err != nil {
		return nil, fmt.Errorf("sendMedia: %w", err)
	}

	doc, msgID, err := extractDocument(updates)
	if err != nil {
		return nil, err
	}
	return &Part{
		MessageID:     msgID,
		DocumentID:    doc.ID,
		AccessHash:    doc.AccessHash,
		FileReference: doc.FileReference,
		Size:          doc.Size,
	}, nil
}

// extractDocument 从不同的 Updates 形态里提取 document。
//
// 输入：MessagesSendMedia 的返回值
// 输出：document、消息 ID、error
// 预期：兼容普通消息、频道消息、短消息三种形态
func extractDocument(u tg.UpdatesClass) (*tg.Document, int, error) {
	switch upd := u.(type) {
	case *tg.Updates:
		for _, item := range upd.Updates {
			var msg *tg.Message
			switch x := item.(type) {
			case *tg.UpdateNewMessage:
				msg, _ = x.Message.(*tg.Message)
			case *tg.UpdateNewChannelMessage:
				msg, _ = x.Message.(*tg.Message)
			}
			if msg == nil {
				continue
			}
			if doc, ok := documentOf(msg.Media); ok {
				return doc, msg.ID, nil
			}
		}
	case *tg.UpdateShortSentMessage:
		if doc, ok := documentOf(upd.Media); ok {
			return doc, upd.ID, nil
		}
	}
	return nil, 0, ErrNoDocument
}

// documentOf 从 MessageMedia 里取 document。
func documentOf(m tg.MessageMediaClass) (*tg.Document, bool) {
	media, ok := m.(*tg.MessageMediaDocument)
	if !ok {
		return nil, false
	}
	doc, ok := media.Document.(*tg.Document)
	return doc, ok
}
