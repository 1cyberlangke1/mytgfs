package telegramstore

import (
	"context"
	"fmt"
	"io"

	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/tg"
)

const (
	// partSize 是单个 getFile 请求的块大小，必须能被 4KB 整除。
	partSize = 1 << 20

	// defaultThreads 是单次下载的并发工作协程上限。
	// 实际在飞请求数由 downloader.Scheduler 的窗口控制。
	defaultThreads = 16
)

// rangeWriterAt 把「按文件绝对偏移写入」的结果落到切片区间里。
//
// 输入：p 一段数据；off 文件内绝对偏移
// 输出：写入字节数
// 预期：越界即失败，绝不静默丢数据
type rangeWriterAt struct {
	buf  []byte
	base int64
}

func (w *rangeWriterAt) WriteAt(p []byte, off int64) (int, error) {
	rel := off - w.base
	if rel < 0 || rel+int64(len(p)) > int64(len(w.buf)) {
		return 0, io.ErrShortWrite
	}
	return copy(w.buf[rel:], p), nil
}

// ReadRange 读取 document 的任意区间。
//
// 输入：p 已存储 Part；offset 起始；length 长度
// 输出：恰好 length 字节
// 预期：
//   - 分块、并发、CDN 重定向、file_reference 过期重试全部交给 gotd downloader
//   - offset 会对齐到 partSize，多取的字节在返回前裁掉
//   - 中断无副作用，重试即可
func (s *Store) ReadRange(ctx context.Context, p *Part, offset int64, length int) ([]byte, error) {
	if offset < 0 || length < 0 || offset+int64(length) > p.Size {
		return nil, ErrBadRange
	}
	if length == 0 {
		return []byte{}, nil
	}

	start := offset / partSize * partSize
	end := offset + int64(length)
	if r := end % partSize; r != 0 {
		end += partSize - r
	}
	if end > p.Size {
		end = p.Size
	}

	buf := make([]byte, end-start)
	loc := &tg.InputDocumentFileLocation{
		ID:            p.DocumentID,
		AccessHash:    p.AccessHash,
		FileReference: p.FileReference,
	}
	// WithSize(end) 让 downloader 在 end 处停止，而不是读到文件末尾
	w := &rangeWriterAt{buf: buf, base: start}
	if _, err := s.dl.Download(s.api, loc).
		WithOffset(start).
		WithSize(end).
		WithThreads(s.threads()).
		Parallel(ctx, w); err != nil {
		return nil, fmt.Errorf("下载 %d..%d: %w", start, end, err)
	}

	lo := offset - start
	return buf[lo : lo+int64(length)], nil
}

// threads 返回下载工作协程上限。
func (s *Store) threads() int {
	if s.threadCount <= 0 {
		return defaultThreads
	}
	return s.threadCount
}

// dl 是 Store 内共享的 downloader，带自适应窗口调度器。
func newDownloader() *downloader.Downloader {
	return downloader.NewDownloader().
		WithPartSize(partSize).
		WithScheduler(downloader.NewScheduler(downloader.SchedulerOptions{
			Min:   1,
			Start: 4,
			Max:   16,
		}))
}
