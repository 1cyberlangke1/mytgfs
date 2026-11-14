package telegramstore

// 频道头像读取：封面不再是卷里的文件，而是频道本身的头像。
//
// 读：channels.getFullChannel → ChannelFull.chatPhoto → 下载最大尺寸。
// 写由 app.SetChannelPhoto 用 user 会话完成（需要 canChangeInfo）。
// 元数据带短缓存，避免每次面板轮询都打一次 full info。

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/gotd/td/tg"

	"mytgfs/internal/i18n"
)

// coverTTL 是头像元数据缓存时长：别人换头像最多这么久被同步轮询发现。
const coverTTL = 30 * time.Second

// ErrNoCover 表示频道当前没有头像。
var ErrNoCover error = errNoCover{}

type errNoCover struct{}

func (errNoCover) Error() string { return i18n.T("err.cover_missing") }

// CoverVersion 返回当前头像的版本号。
//
// 输入：ctx
// 输出：photo id 的十进制字符串；无头像时为空串
// 预期：直接用作前端图片 URL 的 ?v=，换头像后 URL 自然失效
func (s *Store) CoverVersion(ctx context.Context) (string, error) {
	id, _, err := s.coverPhoto(ctx, false)
	if err != nil {
		return "", err
	}
	if id == 0 {
		return "", nil
	}
	return strconv.FormatInt(id, 10), nil
}

// CoverChanged 判断别人是否换过头像。
//
// 输入：ctx
// 输出：true 表示相对上次观测发生了变化
// 预期：
//   - 第一次观测只建立基线，返回 false，避免启动时无谓刷新
//   - 头像被删除也算变化（id 变 0）
func (s *Store) CoverChanged(ctx context.Context) (bool, error) {
	id, _, err := s.coverPhoto(ctx, true)
	if err != nil {
		return false, err
	}
	s.photoMu.Lock()
	defer s.photoMu.Unlock()
	if !s.coverInit {
		s.coverInit = true
		s.coverSeen = id
		return false, nil
	}
	if s.coverSeen == id {
		return false, nil
	}
	s.coverSeen = id
	return true, nil
}

// CoverImage 下载频道头像。
//
// 输入：ctx
// 输出：JPEG/PNG 原始字节；版本号
// 预期：
//   - 取尺寸最大的那一档，避免面板放大后糊
//   - file_reference 过期时强制刷新一次元数据再重试
//   - 频道没有头像返回 ErrNoCover
func (s *Store) CoverImage(ctx context.Context) ([]byte, string, error) {
	data, version, err := s.coverImage(ctx, false)
	if err != nil && IsFileReferenceError(err) {
		// 引用过期：强制重拉 full info 后重试一次
		data, version, err = s.coverImage(ctx, true)
	}
	return data, version, err
}

func (s *Store) coverImage(ctx context.Context, force bool) ([]byte, string, error) {
	id, photo, err := s.coverPhoto(ctx, force)
	if err != nil {
		return nil, "", err
	}
	if id == 0 || photo == nil {
		return nil, "", ErrNoCover
	}
	size, ok := largestPhotoSize(photo.Sizes)
	if !ok {
		return nil, "", ErrNoCover
	}
	buf := make([]byte, size.Size)
	loc := &tg.InputPhotoFileLocation{
		ID:            photo.ID,
		AccessHash:    photo.AccessHash,
		FileReference: photo.FileReference,
		ThumbSize:     size.Type,
	}
	b := s.dl.Download(s.api, loc).
		WithSize(int64(size.Size)).
		WithThreads(s.threads())
	if _, err := b.Parallel(ctx, &rangeWriterAt{buf: buf}); err != nil {
		return nil, "", DescribeError(fmt.Errorf("%s: %w", i18n.T("err.cover_fetch"), s.noteErr(err)))
	}
	return buf, strconv.FormatInt(id, 10), nil
}

// coverPhoto 返回头像 id 与元数据，带 TTL 缓存。
//
// 输入：ctx；force 为 true 时忽略缓存
// 输出：photo id（无头像为 0）；*tg.Photo（无头像为 nil）
// 预期：只缓存成功结果；查询失败不污染缓存
func (s *Store) coverPhoto(ctx context.Context, force bool) (int64, *tg.Photo, error) {
	s.photoMu.Lock()
	if !force && s.photoOK && time.Since(s.photoAt) < coverTTL {
		p := s.photo
		s.photoMu.Unlock()
		if p == nil {
			return 0, nil, nil
		}
		return p.ID, p, nil
	}
	s.photoMu.Unlock()

	ch, err := s.resolve(ctx)
	if err != nil {
		return 0, nil, err
	}
	full, err := s.api.ChannelsGetFullChannel(ctx, &tg.InputChannel{
		ChannelID:  ch.ID,
		AccessHash: ch.AccessHash,
	})
	if err != nil {
		return 0, nil, DescribeError(fmt.Errorf("%s: %w", i18n.T("err.cover_fetch"), s.noteErr(err)))
	}
	var photo *tg.Photo
	if full != nil {
		if cf, ok := full.FullChat.(*tg.ChannelFull); ok {
			if p, ok := cf.GetChatPhoto().(*tg.Photo); ok {
				photo = p
			}
		}
	}

	s.photoMu.Lock()
	s.photo = photo
	s.photoAt = time.Now()
	s.photoOK = true
	if photo == nil {
		s.photoMu.Unlock()
		return 0, nil, nil
	}
	id := photo.ID
	s.photoMu.Unlock()
	return id, photo, nil
}

// largestPhotoSize 取面积最大的尺寸档。
//
// 输入：photo.Sizes
// 输出：选中的 PhotoSize 与是否找到
// 预期：跳过 PhotoSizeEmpty/PhotoStrippedSize 这类无尺寸信息的条目
func largestPhotoSize(sizes []tg.PhotoSizeClass) (tg.PhotoSize, bool) {
	var best tg.PhotoSize
	found := false
	for _, sc := range sizes {
		p, ok := sc.(*tg.PhotoSize)
		if !ok || p.Size <= 0 {
			continue
		}
		if !found || p.W*p.H > best.W*best.H {
			best = *p
			found = true
		}
	}
	return best, found
}
