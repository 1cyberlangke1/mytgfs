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

	"mytgfs/internal/channel"
	"mytgfs/internal/i18n"
)

// 头像元数据缓存没有过期时间：频道 pts 变化（含换头像）由同步循环调用
// InvalidateCover 失效，与第三方客户端一致靠更新流，不轮询。

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
	if size.Size <= 0 || size.Size > maxCoverBytes {
		return nil, "", fmt.Errorf("%s", i18n.T("err.cover_fetch"))
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

// Title 返回频道标题。
//
// 输入：ctx
// 输出：Telegram 侧显示名；频道不可见时返回错误
// 预期：与头像共用 resolve 的频道缓存，频道更新（pts 变化）后由
// InvalidateCover 一并失效，下次读取即回源
func (s *Store) Title(ctx context.Context) (string, error) {
	ch, err := s.resolve(ctx)
	if err != nil {
		return "", err
	}
	return ch.Title, nil
}

// SetTitle 改频道标题（面板上的显示名）。
//
// 输入：title 新标题，去空白后不能为空
// 输出：error；权限不足或频道不可用时返回翻译后的错误
// 预期：调用成功后清空频道缓存，下次 Title 读到新值；
//
//	只改频道标题，不动目录节点
func (s *Store) SetTitle(ctx context.Context, title string) error {
	ch, err := s.resolve(ctx)
	if err != nil {
		return err
	}
	if err := channel.SetTitle(ctx, s.api, ch.ID, ch.AccessHash, title); err != nil {
		return DescribeError(s.noteErr(err))
	}
	s.InvalidateCover()
	return nil
}

// BotUsername 返回 bot 自己的用户名（不含 @）。
//
// 输入：ctx
// 输出：用户名；bot 会话被拒绝或没有用户名时返回错误
// 预期：只读一次 users.getUsers；供 user 会话按用户名解析 bot 再补授权
func (s *Store) BotUsername(ctx context.Context) (string, error) {
	users, err := s.api.UsersGetUsers(ctx, []tg.InputUserClass{&tg.InputUserSelf{}})
	if err != nil {
		return "", err
	}
	for _, u := range users {
		if user, ok := u.(*tg.User); ok {
			if user.Username == "" {
				return "", fmt.Errorf("%s", i18n.T("err.bot_no_username"))
			}
			return user.Username, nil
		}
	}
	return "", fmt.Errorf("%s", i18n.T("err.bot_not_found"))
}

// InvalidateCover 让头像与频道元数据缓存立即失效。
//
// 输入：无
// 输出：无
// 预期：外部会话写头像、或同步循环看到频道更新（标题/头像/权限）后调用；
// 下一次 CoverVersion/CoverImage/Title 强制回源
func (s *Store) InvalidateCover() {
	s.photoMu.Lock()
	s.photoOK = false
	s.photo = nil
	s.photoAt = time.Time{}
	s.photoMu.Unlock()
	// 频道标题也算元数据：外部改了频道名，面板要跟着变
	s.mu.Lock()
	s.channel = nil
	s.mu.Unlock()
}

// coverPhoto 返回头像 id 与元数据，带 TTL 缓存。
//
// 输入：ctx；force 为 true 时忽略缓存
// 输出：photo id（无头像为 0）；*tg.Photo（无头像为 nil）
// 预期：只缓存成功结果；查询失败不污染缓存
func (s *Store) coverPhoto(ctx context.Context, force bool) (int64, *tg.Photo, error) {
	s.photoMu.Lock()
	if !force && s.photoOK {
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
