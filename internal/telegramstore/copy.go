package telegramstore

// 频道内复制：不重新上传字节，只发一条引用同一 document 的新消息。
//
// 用途有两个：
//   - 复制文件（目标路径与源不同）
//   - 去重命中时把已有 document 再挂到新路径上，省掉整份上传
//
// 边界：
//   - 源 document 的 file_reference 过期时先按消息 ID 刷新再重试一次
//   - 复制只新增一条消息，源消息保持不动；调用方负责删掉目标路径的旧消息

import (
	"context"
	"fmt"
	"math/rand"

	"github.com/gotd/td/tg"
)

// Copy 在频道内复制已有文件，不传输文件内容。
//
// 输入：src 源 document 所在的记录；name 文件名；mime MIME；caption 新消息说明文字
// 输出：新消息的 Part（document 与 src 相同，消息 ID 是新的）
// 预期：file_reference 失效时刷新后重试一次；其它错误原样返回
func (s *Store) Copy(ctx context.Context, src *Part, name, mime, caption string) (*Part, error) {
	if src == nil || src.DocumentID == 0 {
		return nil, ErrNoDocument
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

	part, err := s.sendDocRef(ctx, peer, src, name, mime, caption)
	if err == nil || !IsFileReferenceError(err) {
		return part, err
	}
	np, rerr := s.Refresh(ctx, src.MessageID)
	if rerr != nil {
		return nil, fmt.Errorf("复制前刷新 file_reference: %w", rerr)
	}
	return s.sendDocRef(ctx, peer, np, name, mime, caption)
}

// sendDocRef 发一条引用已有 document 的消息。
//
// 输入：ctx、peer、src、name/mime/caption
// 输出：新消息的 Part、error
// 预期：不调用 uploader，网络里只走一次 sendMedia
func (s *Store) sendDocRef(ctx context.Context, peer tg.InputPeerClass, src *Part, name, mime, caption string) (*Part, error) {
	if err := s.throttle(ctx); err != nil {
		return nil, err
	}
	updates, err := s.api.MessagesSendMedia(ctx, &tg.MessagesSendMediaRequest{
		Peer: peer,
		Media: &tg.InputMediaDocument{
			ID: &tg.InputDocument{
				ID:            src.DocumentID,
				AccessHash:    src.AccessHash,
				FileReference: src.FileReference,
			},
		},
		Message:  caption,
		RandomID: rand.Int63(),
	})
	if err != nil {
		return nil, fmt.Errorf("copy: sendMedia: %w", err)
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
