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
	part, _, err := s.copyWithRefresh(ctx, s, src, name, mime, caption)
	return part, err
}

// CopyFrom 把别的卷里的文件复制到 s 所在频道。
//
// 输入：ref 源文件所在卷的 Store（只用于刷新 file_reference）；src 源记录；name/mime/caption 同 Copy
// 输出：新消息 Part；refreshed 为刷新后的源 Part（未刷新时为 nil，调用方应写回源索引）
// 预期：消息发到 s 的频道，源频道不变；跨频道时 file_reference 可能失效，必须用源 Store 刷新
func (s *Store) CopyFrom(ctx context.Context, ref *Store, src *Part, name, mime, caption string) (*Part, *Part, error) {
	return s.copyWithRefresh(ctx, ref, src, name, mime, caption)
}

// copyWithRefresh 是 Copy/CopyFrom 的共同实现。
//
// 输入：ref 负责刷新 file_reference 的 Store；src 源记录；name/mime/caption
// 输出：目标频道的新消息 Part、刷新后的源 Part（无刷新为 nil）、error
// 预期：同一账号内 document 的 access_hash 通用，跨频道只需一条 sendMedia
func (s *Store) copyWithRefresh(ctx context.Context, ref *Store, src *Part, name, mime, caption string) (*Part, *Part, error) {
	if src == nil || src.DocumentID == 0 {
		return nil, nil, ErrNoDocument
	}
	ch, err := s.resolve(ctx)
	if err != nil {
		return nil, nil, err
	}
	peer := &tg.InputPeerChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash}
	caption, err = s.prepareCaption(ctx, peer, caption)
	if err != nil {
		return nil, nil, err
	}

	part, err := s.sendDocRef(ctx, peer, src, name, mime, caption)
	if err == nil || !IsFileReferenceError(err) {
		return part, nil, err
	}
	np, rerr := ref.Refresh(ctx, src.MessageID)
	if rerr != nil {
		return nil, nil, fmt.Errorf("复制前刷新 file_reference: %w", rerr)
	}
	part, err = s.sendDocRef(ctx, peer, np, name, mime, caption)
	return part, np, err
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
		return nil, DescribeError(fmt.Errorf("copy: sendMedia: %w", s.noteErr(err)))
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
