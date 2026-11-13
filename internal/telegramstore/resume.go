package telegramstore

// 断点续传上传。
//
// Telegram 的上传协议是按 (file_id, file_part) 逐片存到服务端临时区，
// 分片可以乱序、重复发送，只要在临时区过期前把 0..n-1 片补齐即可。
// 因此续传需要三样东西：
//   - 稳定的 file_id：由调用方按内容决定，重启后仍指向同一份临时文件
//   - 已发分片位图：持久化到本地，重试时跳过
//   - 分片大小：必须与首次一致，否则 FILE_PARTS_INVALID
//
// 大文件阈值与 Telegram 一致：<=10MB 用 upload.saveFilePart，
// 更大的用 upload.saveBigFilePart（需要带总分片数）。

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"math/rand"
	"slices"
	"sync"
	"time"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"mytgfs/internal/i18n"
)

const (
	// uploadMinPartSize 是协议允许的最小小片（也是 gotd 的默认值）。
	uploadMinPartSize = 128 * 1024
	// uploadMaxPartSize 是协议允许的最大片大小。
	uploadMaxPartSize = 512 * 1024
	// uploadMaxParts 是 upload.saveFilePart 的分片数上限（0..3999）。
	uploadMaxParts = 4000
	// uploadBigLimit 决定用 saveFilePart 还是 saveBigFilePart。
	uploadBigLimit = 10 * 1024 * 1024
	// defaultUploadWorkers 是没配置 upload_parallel 时的并发分片数。
	defaultUploadWorkers = 4
	// resumeSaveInterval 是断点落盘间隔：按时间而不是片数，
	// 网络快时少写盘、慢时不丢太多进度。
	resumeSaveInterval = 2 * time.Second
)

// UploadResume 是一次未完成上传的断点。
type UploadResume struct {
	// FileID 是 Telegram 侧临时文件 ID，续传必须沿用。
	FileID int64 `json:"file_id"`
	// PartSize 是首次上传时的分片大小，续传必须一致。
	PartSize int `json:"part_size"`
	// TotalParts 是总分片数，兼容 file_id 复用时校验。
	TotalParts int `json:"total_parts"`
	// Done 是已确认写入的分片序号，升序。
	Done []int `json:"done"`
}

// ResumeStore 持久化上传断点。
//
// 输入：key 由调用方决定（建议内容 sha256）；输出：断点或 nil
// 预期：实现要能跨进程重启存活；文件不存在视为没有断点
type ResumeStore interface {
	Load(key string) (*UploadResume, error)
	Save(key string, r *UploadResume) error
	Clear(key string) error
}

// SetResumeStore 注入断点存储。
//
// 输入：rs 断点存储，可为 nil（此时退化为一次性上传）
// 输出：无
// 预期：只影响 UploadResumable，普通 Upload 行为不变
func (s *Store) SetResumeStore(rs ResumeStore) { s.resumeStore = rs }

// UploadResumable 上传文件并在中断后从断点继续。
//
// 输入：r 随机读源；size 字节数；key 断点键（空表示不续传）；name/mime/caption 同 Upload
// 输出：新消息的 Part
// 预期：
//   - 断点存在且分片大小一致时跳过已完成分片
//   - 断点失效（临时区过期/片数不符）时清空重传一次
//   - sendMedia 成功后才清断点，所以发送失败也能只重发最后一步
func (s *Store) UploadResumable(ctx context.Context, r io.ReaderAt, size int64, key, name, mime, caption string) (*Part, error) {
	if size < 0 || size > MaxFileSize {
		return nil, ErrTooLarge
	}
	ch, err := s.resolve(ctx)
	if err != nil {
		return nil, err
	}
	peer := &tg.InputPeerChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash}

	partSize := uploadPartSizeFor(size)
	total := partsOf(size, partSize)
	resume, done := s.loadResume(key, partSize, total)
	if resume == nil {
		resume, done = s.newResume(partSize, total)
	} else if len(done) > 0 {
		slog.Debug(i18n.T("log.upload_resume", len(done), total), "key", key)
	}

	if err := s.sendParts(ctx, r, size, resume, done, key); err != nil {
		if !IsUploadStateError(err) {
			return nil, err
		}
		// 服务端临时区已过期或分片状态对不上：清断点整份重传一次
		s.clearResume(key)
		resume, done = s.newResume(partSize, total)
		if err := s.sendParts(ctx, r, size, resume, done, key); err != nil {
			return nil, err
		}
	}
	part, err := s.sendUploaded(ctx, peer, resume, size, name, mime, caption)
	if err == nil {
		s.clearResume(key)
		return part, nil
	}
	if !IsUploadStateError(err) {
		return nil, err
	}
	// 服务端临时文件过期或片数不符：清断点，整份重传一次
	s.clearResume(key)
	resume, done = s.newResume(partSize, total)
	if err := s.sendParts(ctx, r, size, resume, done, key); err != nil {
		return nil, err
	}
	part, err = s.sendUploaded(ctx, peer, resume, size, name, mime, caption)
	if err != nil {
		return nil, err
	}
	s.clearResume(key)
	return part, nil
}

// IsUploadStateError 判断错误是否说明上传临时文件状态不可用。
//
// 输入：任意 error
// 输出：true 表示清空断点后整份重传可能成功
// 预期：覆盖临时区过期、片数不符、分片缺失这几类
func IsUploadStateError(err error) bool {
	e, ok := errors.AsType[*tgerr.Error](err)
	if !ok {
		return false
	}
	switch e.Type {
	case "FILE_PART_INVALID", "FILE_PART_MISSING", "FILE_PART_EMPTY",
		"FILE_PARTS_INVALID", "FILE_ID_INVALID", "PART_SIZE_INVALID", "MEDIA_INVALID":
		return true
	}
	return false
}

// newResume 生成一份新的断点。
//
// 输入：total 总分片数
// 输出：断点与空位图
// 预期：file_id 随机但为正数，Telegram 只要求不要与在传文件撞号
func (s *Store) newResume(partSize, total int) (*UploadResume, map[int]bool) {
	id := rand.Int63()
	if id == 0 {
		id = 1
	}
	return &UploadResume{FileID: id, PartSize: partSize, TotalParts: total}, make(map[int]bool)
}

// uploadPartSizeFor 按文件大小推导分片大小。
//
// 输入：size 文件字节数
// 输出：128KB..512KB 之间的值，且总分片数不超过 4000
// 预期：从协议允许的最小片起步，装不下再翻倍；结果只由 size 决定，续传能复现
func uploadPartSizeFor(size int64) int {
	part := uploadMinPartSize
	for part < uploadMaxPartSize && partsOf(size, part) > uploadMaxParts {
		part *= 2
	}
	return part
}

// partsOf 计算分片数。
func partsOf(size int64, partSize int) int {
	if size <= 0 {
		return 0
	}
	n := int(size / int64(partSize))
	if size%int64(partSize) != 0 {
		n++
	}
	return n
}

// SetUploadParallel 设置上传并发分片数。
//
// 输入：n 并发数；<=0 用默认值，>16 按 16 处理
// 输出：无
// 预期：网络好的用户可以调大，慢网络调小；与下载并发独立
func (s *Store) SetUploadParallel(n int) {
	switch {
	case n <= 0:
		s.uploadWorkers = defaultUploadWorkers
	case n > 16:
		s.uploadWorkers = 16
	default:
		s.uploadWorkers = n
	}
}

// workers 返回上传并发数。
func (s *Store) workers() int {
	if s.uploadWorkers <= 0 {
		return defaultUploadWorkers
	}
	return s.uploadWorkers
}

// loadResume 读取可用断点。
//
// 输入：key 断点键；total 本次总分片数
// 输出：断点、已完成位图；不可用时返回 nil
// 预期：分片大小或总分片数不一致视为不可用，避免把两段数据拼在一起
func (s *Store) loadResume(key string, partSize, total int) (*UploadResume, map[int]bool) {
	if s.resumeStore == nil || key == "" {
		return nil, nil
	}
	r, err := s.resumeStore.Load(key)
	if err != nil || r == nil || r.FileID == 0 {
		return nil, nil
	}
	if r.PartSize != partSize || r.TotalParts != total {
		return nil, nil
	}
	done := make(map[int]bool, len(r.Done))
	for _, i := range r.Done {
		if i >= 0 && i < total {
			done[i] = true
		}
	}
	return r, done
}

// saveResume 落一次断点。
//
// 输入：key、断点、位图
// 输出：无
// 预期：位图转成升序切片写盘；失败只打印，不打断上传
func (s *Store) saveResume(key string, r *UploadResume, done map[int]bool) {
	if s.resumeStore == nil || key == "" {
		return
	}
	r.Done = slices.Sorted(maps.Keys(done))
	if err := s.resumeStore.Save(key, r); err != nil {
		slog.Debug(i18n.T("log.resume_save"), "err", err)
	}
}

// clearResume 删除断点。
func (s *Store) clearResume(key string) {
	if s.resumeStore == nil || key == "" {
		return
	}
	if err := s.resumeStore.Clear(key); err != nil {
		slog.Debug(i18n.T("log.resume_delete"), "err", err)
	}
}

// sendParts 并发上传所有未完成分片。
//
// 输入：r 随机读源；size 总字节；resume 断点（含 file_id）；done 已完成位图；key 断点键
// 输出：error；任一分片失败即取消其余分片
// 预期：顺序无关，Telegram 按 file_part 组装；断点按 resumeSaveInterval 定时落盘
func (s *Store) sendParts(ctx context.Context, r io.ReaderAt, size int64, resume *UploadResume, done map[int]bool, key string) error {
	total := resume.TotalParts
	partSize := resume.PartSize
	big := size > uploadBigLimit
	lastSave := time.Now()

	var (
		mu       sync.Mutex
		firstErr error
		doneN    int
	)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	jobs := make(chan int)
	var wg sync.WaitGroup
	workers := min(total, s.workers())
	for range workers {
		wg.Go(func() {
			buf := make([]byte, partSize)
			for idx := range jobs {
				off := int64(idx) * int64(partSize)
				n := partSize
				if off+int64(n) > size {
					n = int(size - off)
				}
				data := buf[:n]
				if _, err := r.ReadAt(data, off); err != nil && !errors.Is(err, io.EOF) {
					mu.Lock()
					if firstErr == nil {
						firstErr = fmt.Errorf("%s: %w", i18n.T("err.upload_part_read", idx), err)
					}
					mu.Unlock()
					cancel()
					return
				}
				if err := s.savePart(ctx, resume.FileID, idx, total, data, big); err != nil {
					mu.Lock()
					if firstErr == nil {
						firstErr = fmt.Errorf("%s: %w", i18n.T("err.upload_part", idx), err)
					}
					mu.Unlock()
					cancel()
					return
				}
				mu.Lock()
				done[idx] = true
				doneN++
				// 按时间落盘：快网络少写盘，慢网络也不会丢太多进度
				due := time.Since(lastSave) >= resumeSaveInterval
				if due {
					lastSave = time.Now()
					s.saveResume(key, resume, done)
				}
				mu.Unlock()
			}
		})
	}

	for idx := range total {
		mu.Lock()
		skip := done[idx]
		mu.Unlock()
		if skip {
			continue
		}
		select {
		case jobs <- idx:
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			mu.Lock()
			err := firstErr
			s.saveResume(key, resume, done)
			mu.Unlock()
			if err == nil {
				err = ctx.Err()
			}
			return err
		}
	}
	close(jobs)
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if firstErr != nil {
		s.saveResume(key, resume, done)
		return firstErr
	}
	s.saveResume(key, resume, done)
	return nil
}

// savePart 发送单个分片。
//
// 输入：fileID、idx 分片序号、total 总分片数、data 分片内容、big 是否走大文件接口
// 输出：error
// 预期：服务端返回 false 视为失败；FLOOD_WAIT 由 gotd 中间件处理
func (s *Store) savePart(ctx context.Context, fileID int64, idx, total int, data []byte, big bool) error {
	if big {
		ok, err := s.api.UploadSaveBigFilePart(ctx, &tg.UploadSaveBigFilePartRequest{
			FileID:         fileID,
			FilePart:       idx,
			FileTotalParts: total,
			Bytes:          data,
		})
		if err != nil {
			return err
		}
		if !ok {
			return errors.New(i18n.T("err.upload_part_rejected"))
		}
		return nil
	}
	ok, err := s.api.UploadSaveFilePart(ctx, &tg.UploadSaveFilePartRequest{
		FileID:   fileID,
		FilePart: idx,
		Bytes:    data,
	})
	if err != nil {
		return err
	}
	if !ok {
		return errors.New(i18n.T("err.upload_part_rejected"))
	}
	return nil
}

// sendUploaded 把已上传的分片挂成频道消息。
//
// 输入：peer 目标频道；resume 断点（含 file_id 与片数）；size/name/mime/caption
// 输出：Part
// 预期：只发一次 sendMedia，不再传字节
func (s *Store) sendUploaded(ctx context.Context, peer tg.InputPeerClass, resume *UploadResume, size int64, name, mime, caption string) (*Part, error) {
	if err := s.throttle(ctx); err != nil {
		return nil, err
	}
	var file tg.InputFileClass
	if size > uploadBigLimit {
		file = &tg.InputFileBig{ID: resume.FileID, Parts: resume.TotalParts, Name: name}
	} else {
		file = &tg.InputFile{ID: resume.FileID, Parts: resume.TotalParts, Name: name}
	}
	updates, err := s.api.MessagesSendMedia(ctx, &tg.MessagesSendMediaRequest{
		Peer: peer,
		Media: &tg.InputMediaUploadedDocument{
			File:     file,
			MimeType: mime,
			Attributes: []tg.DocumentAttributeClass{
				&tg.DocumentAttributeFilename{FileName: name},
			},
		},
		Message:  caption,
		RandomID: rand.Int63(),
	})
	if err != nil {
		return nil, err
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
