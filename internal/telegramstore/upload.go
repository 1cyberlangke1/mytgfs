package telegramstore

// 发送文档消息的返回值解析：把 MessagesSendMedia 的 Updates 转成 Part。
// 供 copy.go / resume.go 共用；上传入口是 UploadResumable。

import (
	"github.com/gotd/td/tg"
)

// partFromUpdates 把「发送文档消息」的返回转成 Part。
//
// 输入：MessagesSendMedia 的返回值
// 输出：Part；返回值里没有文档时返回 ErrNoDocument
// 预期：只取文档本体与消息号，缩略图存 stripping 后的可持久化形态
func partFromUpdates(updates tg.UpdatesClass) (*Part, error) {
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
		Thumb:         strippedOf(doc),
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
