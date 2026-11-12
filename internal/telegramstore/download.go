package telegramstore

import (
	"context"
	"fmt"

	"github.com/gotd/td/tg"
)

const maxChunk = 1 << 20 // upload.getFile precise 单次上限 1MB

// ReadRange 读取 document 的任意区间。
//
// 输入：p 已存储 Part；offset 起始；length 长度
// 输出：恰好 length 字节
// 预期：
//   - 内部按 1MB 块拆分，满足 offset/limit 不跨块
//   - 中断无副作用，重试即可
func (s *Store) ReadRange(ctx context.Context, p *Part, offset int64, length int) ([]byte, error) {
	if offset < 0 || length < 0 || offset+int64(length) > p.Size {
		return nil, ErrBadRange
	}
	out := make([]byte, 0, length)
	for len(out) < length {
		cur := offset + int64(len(out))
		remain := maxChunk - int(cur%maxChunk)
		want := length - len(out)
		if want > remain {
			want = remain
		}
		b, err := s.readChunk(ctx, p, cur, want)
		if err != nil {
			return nil, err
		}
		out = append(out, b...)
	}
	return out, nil
}

// readChunk 读取单个不超过 1MB、不跨块的区间。
//
// upload.getFile 的 precise 模式要求 offset 和 limit 都按 1KB 对齐，
// 所以这里先对齐、读回后再截取调用方需要的部分。
func (s *Store) readChunk(ctx context.Context, p *Part, offset int64, length int) ([]byte, error) {
	const align = 1024

	alignedOffset := offset / align * align
	end := offset + int64(length)
	alignedEnd := (end + align - 1) / align * align
	// 不跨 1MB 块；允许超过文件末尾（limit 必须是 1KB 倍数）
	blockEnd := (alignedOffset/(1<<20) + 1) * (1 << 20)
	if alignedEnd > blockEnd {
		alignedEnd = blockEnd
	}
	alignedLen := int(alignedEnd - alignedOffset)
	if alignedLen <= 0 {
		return nil, ErrBadRange
	}

	req := &tg.UploadGetFileRequest{
		Location: &tg.InputDocumentFileLocation{
			ID:            p.DocumentID,
			AccessHash:    p.AccessHash,
			FileReference: p.FileReference,
			ThumbSize:     "",
		},
		Offset: alignedOffset,
		Limit:  alignedLen,
	}
	req.SetPrecise(true)
	req.SetFlags()

	res, err := s.api.UploadGetFile(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("getFile: %w", err)
	}
	f, ok := res.(*tg.UploadFile)
	if !ok {
		return nil, fmt.Errorf("unexpected upload result %T", res)
	}
	start := int(offset - alignedOffset)
	if start+length > len(f.Bytes) {
		return nil, fmt.Errorf("short read: got %d need %d", len(f.Bytes), start+length)
	}
	return f.Bytes[start : start+length], nil
}
