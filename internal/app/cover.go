package app

// 频道头像的装配：面板只认卷名，这里负责找到该卷并写回 Telegram。

import (
	"context"
	"errors"

	"mytgfs/internal/telegramstore"
	"mytgfs/internal/volume"
)

// CoverStore 是卷级头像读取依赖（*telegramstore.Store 实现）。
type CoverStore interface {
	CoverVersion(ctx context.Context) (string, error)
	CoverImage(ctx context.Context) ([]byte, string, error)
	InvalidateCover()
}

// coverVolume 是封面路由需要的最小卷信息。
type coverVolume struct {
	store  CoverStore
	chatID int64 // Telegram 频道 id（非 -100... 形式）
}

// CoverProvider 把面板的封面请求路由到具体卷。
//
// 输入：lookup 按卷名找卷；set 写频道头像（user 会话）
// 输出：实现 web.CoverSource 的四个方法
// 预期：卷被删/未挂载时 HasVolume 返回 false，面板显示 404 而不是 500
type CoverProvider struct {
	lookup func(name string) (coverVolume, bool)
	set    func(ctx context.Context, channelID int64, jpeg []byte) error
}

// NewCoverProvider 构造封面路由。
//
// 输入：a 装配根（写头像用 user 会话）；lookup 卷查找函数
// 输出：*CoverProvider
func NewCoverProvider(a *App, lookup func(name string) (*VolumeFS, bool)) *CoverProvider {
	return &CoverProvider{
		lookup: func(name string) (coverVolume, bool) {
			v, ok := lookup(name)
			if !ok || v == nil {
				return coverVolume{}, false
			}
			return coverVolume{
				store:  v.Store,
				chatID: volume.NormalizeChatID(v.Volume.ChatID),
			}, true
		},
		set: func(ctx context.Context, channelID int64, jpeg []byte) error {
			return a.SetChannelPhoto(ctx, channelID, 0, jpeg)
		},
	}
}

// HasVolume 判断卷当前是否可访问。
func (p *CoverProvider) HasVolume(name string) bool {
	_, ok := p.lookup(name)
	return ok
}

// CoverVersion 返回频道头像版本。
func (p *CoverProvider) CoverVersion(ctx context.Context, vol string) (string, error) {
	v, ok := p.lookup(vol)
	if !ok {
		return "", nil
	}
	return v.store.CoverVersion(ctx)
}

// CoverImage 返回频道头像字节；没有头像时返回空结果而不是错误。
func (p *CoverProvider) CoverImage(ctx context.Context, vol string) ([]byte, string, error) {
	v, ok := p.lookup(vol)
	if !ok {
		return nil, "", nil
	}
	data, version, err := v.store.CoverImage(ctx)
	if errors.Is(err, telegramstore.ErrNoCover) {
		return nil, "", nil
	}
	return data, version, err
}

// SetCover 写入频道头像。
func (p *CoverProvider) SetCover(ctx context.Context, vol string, data []byte) error {
	v, ok := p.lookup(vol)
	if !ok {
		return ErrVolumeNotFound{Name: vol}
	}
	if err := p.set(ctx, v.chatID, data); err != nil {
		return err
	}
	// 独立 user 会话写成功后清掉本卷 Store 的头像元数据缓存，
	// 使下一次 CoverVersion 立即拿到新的 photo id
	v.store.InvalidateCover()
	return nil
}
