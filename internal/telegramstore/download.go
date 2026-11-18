package telegramstore

import (
	"context"
	"fmt"

	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/tg"

	"mytgfs/internal/i18n"
)

const (
	// partSize 是单个 getFile 请求的块大小，必须能被 4KB 整除。
	partSize = 1 << 20

	// defaultThreads 是单次下载的并发工作协程上限。
	// 实际在飞请求数由 downloader.Scheduler 的窗口控制。
	defaultThreads = 16
)

// rangeWriterAt 把「按文件绝对偏移写入」的结果落到调用方要的那一段里。
//
// 输入：p 一段数据；off 文件内绝对偏移
// 输出：写入字节数（区间外的字节被丢弃，不报错）
// 预期：downloader 会按 partSize 对齐读取，首尾块可能超出请求区间；
// 只保留 [base, base+len(buf)) 部分，避免为对齐多分配最多一个 partSize 的内存。
type rangeWriterAt struct {
	buf  []byte
	base int64
}

func (w *rangeWriterAt) WriteAt(p []byte, off int64) (int, error) {
	// 请求区间之外的整块（对齐多读的部分）直接丢掉
	rel := off - w.base
	if rel >= int64(len(w.buf)) || rel+int64(len(p)) <= 0 {
		return len(p), nil
	}
	// 首尾块裁剪到区间内
	skip := 0
	if rel < 0 {
		skip = int(-rel)
		rel = 0
	}
	copy(w.buf[rel:], p[skip:])
	return len(p), nil
}

// ReadRange 读取 document 的任意区间。
//
// 输入：p 已存储 Part；offset 起始；length 长度；progress 可选的已下载字节回调
// 输出：恰好 length 字节
// 预期：
//   - 分块、并发、CDN 重定向、file_reference 过期重试全部交给 gotd downloader
//   - offset 会对齐到 partSize，多取的字节在返回前裁掉
//   - progress 收到的是文件内绝对偏移，来自 downloader 的实际写入进度，不是调用方估算
//   - 中断无副作用，重试即可
func (s *Store) ReadRange(ctx context.Context, p *Part, offset int64, length int, progress func(done int64)) ([]byte, error) {
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

	// buf 只按调用方要的 length 分配；对齐多读的字节由 rangeWriterAt 丢弃
	buf := make([]byte, length)
	loc := &tg.InputDocumentFileLocation{
		ID:            p.DocumentID,
		AccessHash:    p.AccessHash,
		FileReference: p.FileReference,
	}
	// WithSize(end) 让 downloader 在 end 处停止，而不是读到文件末尾
	w := &rangeWriterAt{buf: buf, base: offset}
	b := s.dl.Download(s.api, loc).
		WithOffset(start).
		WithSize(end).
		WithThreads(s.threads())
	if progress != nil {
		// gotd 的 done 是本次下载区间内已写入的字节数，换成文件内绝对偏移
		b = b.WithProgress(func(done, _ int64) { progress(start + done) })
	}
	if _, err := b.Parallel(ctx, w); err != nil {
		return nil, DescribeError(fmt.Errorf(i18n.T("err.download_range")+": %w", start, end, s.noteErr(err)))
	}

	return buf, nil
}

// threads 返回下载工作协程上限。
//
// 运行期可在设置里改，用原子量读，避免与在飞下载的数据竞争。
func (s *Store) threads() int {
	if n := int(s.threadCount.Load()); n > 0 {
		return n
	}
	return defaultThreads
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
