package telegramstore

import (
	"context"
	"fmt"
	"io"
	"math/rand"

	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"
)

const maxCaption = 900 // 留余量，Telegram caption 上限 1024

// Upload 上传文件并发送到频道。
//
// 输入：r 数据源；size 字节数；name 文件名；mime MIME；caption 说明文字
// 输出：Part
// 预期：
//   - size > MaxFileSize 立即返回 ErrTooLarge
//   - caption 超过 900 字符时，先发一条文本消息存完整元数据，文件 caption 只存 {"ref":id}
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

	peer := &tg.InputPeerChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash}
	caption, err = s.prepareCaption(ctx, peer, caption)
	if err != nil {
		return nil, err
	}

	up := uploader.NewUploader(s.api).WithPartSize(512 * 1024)
	inputFile, err := up.Upload(ctx, uploader.NewUpload(name, r, size))
	if err != nil {
		return nil, err
	}

	if err := s.throttle(ctx); err != nil {
		return nil, err
	}
	updates, err := s.api.MessagesSendMedia(ctx, &tg.MessagesSendMediaRequest{
		Peer: peer,
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
		return nil, DescribeError(s.noteErr(err))
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

// prepareCaption 把超长 caption 拆成「元信息文本消息 + 短引用」。
//
// 输入：ctx、peer 目标频道、caption 完整说明文字
// 输出：实际写到文件消息上的 caption
// 预期：不超过 900 字符时原样返回；超过时先发文本消息再返回 {"ref":id}
func (s *Store) prepareCaption(ctx context.Context, peer tg.InputPeerClass, caption string) (string, error) {
	if len(caption) <= maxCaption {
		return caption, nil
	}
	refID, err := s.sendMeta(ctx, peer, caption)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`{"ref":%d}`, refID), nil
}

// sendMeta 发送一条文本消息，返回消息 ID。
func (s *Store) sendMeta(ctx context.Context, peer tg.InputPeerClass, text string) (int, error) {
	if err := s.throttle(ctx); err != nil {
		return 0, err
	}
	updates, err := s.api.MessagesSendMessage(ctx, &tg.MessagesSendMessageRequest{
		Peer:     peer,
		Message:  text,
		RandomID: rand.Int63(),
	})
	if err != nil {
		return 0, DescribeError(s.noteErr(err))
	}
	return extractMessageID(updates)
}

// extractMessageID 从 Updates 里提取消息 ID。
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
