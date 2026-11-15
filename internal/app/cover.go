package app

// 频道头像的装配：面板只认卷名，这里负责找到该卷并写回 Telegram。
//
// 字节走双层缓存：内存（TTL 保鲜）+ 磁盘（data/thumbs/cover-<chat>-<photoID>.jpg）。
// Store 只有元数据缓存，图片字节每次都从 Telegram 现下载，面板才会一直「加载中」；
// 这里缓存字节后，常规路径零 Telegram 往返，启动时直接吃磁盘。

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"

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

// coverEntry 是单卷头像的字节缓存。
type coverEntry struct {
	version string
	jpeg    []byte
}

// CoverProvider 把面板的封面请求路由到具体卷。
//
// 字节缓存没有过期时间：第三方客户端也不轮询头像，
// 靠频道更新流（pts 变化）失效重建，自己写头像时同样失效。
// 输入：lookup 按卷名找卷；set 写频道头像（user 会话）；cacheDir 字节缓存目录（空只用内存）
// 输出：实现 web.CoverSource 的四个方法
// 预期：卷被删/未挂载时 HasVolume 返回 false，面板显示 404 而不是 500
type CoverProvider struct {
	lookup   func(name string) (coverVolume, bool)
	set      func(ctx context.Context, channelID int64, jpeg []byte) error
	cacheDir string

	mu    sync.Mutex
	cache map[int64]coverEntry
}

// NewCoverProvider 构造封面路由。
func NewCoverProvider(a *App, lookup func(name string) (*VolumeFS, bool), cacheDir string) *CoverProvider {
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
		cacheDir: cacheDir,
		cache:    make(map[int64]coverEntry),
	}
}

// HasVolume 判断卷当前是否可访问。
func (p *CoverProvider) HasVolume(name string) bool {
	_, ok := p.lookup(name)
	return ok
}

// CoverVersion 返回频道头像版本；缓存命中时零 Telegram 往返。
func (p *CoverProvider) CoverVersion(ctx context.Context, vol string) (string, error) {
	v, ok := p.lookup(vol)
	if !ok {
		return "", nil
	}
	p.mu.Lock()
	entry, hit := p.cache[v.chatID]
	p.mu.Unlock()
	if hit {
		return entry.version, nil
	}
	return v.store.CoverVersion(ctx)
}

// CoverImage 返回频道头像字节；没有头像时返回空结果而不是错误。
//
// 读取顺序：内存缓存 → 磁盘（冷启动免回源）→ Telegram；
// 回源失败时用内存/磁盘旧图顶上，面板不白屏。
// 缓存永不过期：频道 pts 变化（含换头像）由同步循环触发 InvalidateVolume 失效重建。
func (p *CoverProvider) CoverImage(ctx context.Context, vol string) ([]byte, string, error) {
	cv, ok := p.lookup(vol)
	if !ok {
		return nil, "", nil
	}

	p.mu.Lock()
	entry, hit := p.cache[cv.chatID]
	p.mu.Unlock()
	if hit {
		return entry.jpeg, entry.version, nil
	}

	// 冷启动：磁盘上有上一张就直接用，不阻塞面板
	if p.cacheDir != "" {
		if data, version, found := p.diskCover(cv.chatID); found {
			p.remember(cv.chatID, version, data)
			return data, version, nil
		}
	}

	data, version, err := cv.store.CoverImage(ctx)
	if err != nil {
		if errors.Is(err, telegramstore.ErrNoCover) {
			return nil, "", nil
		}
		// Telegram 挂了：有旧图就顶上
		if p.cacheDir != "" {
			if data, version, found := p.diskCover(cv.chatID); found {
				p.remember(cv.chatID, version, data)
				return data, version, nil
			}
		}
		return nil, "", err
	}
	p.remember(cv.chatID, version, data)
	return data, version, nil
}

// InvalidateVolume 使该卷的头像缓存失效（同步循环发现频道更新时调用）。
//
// 输入：vol 卷名
// 输出：无
// 预期：下一次 CoverVersion/CoverImage 重新回源元数据；photo id 变了才重下字节
func (p *CoverProvider) InvalidateVolume(vol string) {
	v, ok := p.lookup(vol)
	if !ok {
		return
	}
	p.mu.Lock()
	delete(p.cache, v.chatID)
	p.mu.Unlock()
}

// SetCover 写入频道头像并让本卷的缓存立即失效。
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
	p.drop(v.chatID)
	return nil
}

// remember 更新内存缓存并把字节落盘（版本化文件名，旧版本顺手清掉）。
func (p *CoverProvider) remember(chatID int64, version string, jpeg []byte) {
	p.mu.Lock()
	p.cache[chatID] = coverEntry{version: version, jpeg: jpeg}
	p.mu.Unlock()
	if p.cacheDir == "" || len(jpeg) == 0 || version == "" {
		return
	}
	target := filepath.Join(p.cacheDir, p.fileName(chatID, version))
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, jpeg, 0o600); err != nil {
		return
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return
	}
	// 只留最新版本：旧 photo id 的文件删掉防堆积
	if old, _ := filepath.Glob(filepath.Join(p.cacheDir, p.fileName(chatID, "*"))); len(old) > 1 {
		for _, f := range old {
			if f != target {
				_ = os.Remove(f)
			}
		}
	}
}

// drop 清掉内存缓存；磁盘文件保留当离线兜底。
func (p *CoverProvider) drop(chatID int64) {
	p.mu.Lock()
	delete(p.cache, chatID)
	p.mu.Unlock()
}

// fileName 生成版本化的缓存文件名。
func (p *CoverProvider) fileName(chatID int64, version string) string {
	return "cover-" + strconv.FormatInt(chatID, 10) + "-" + version + ".jpg"
}

// diskCover 从磁盘找该卷最新的头像缓存。
//
// 输入：chatID 频道 id
// 输出：字节、版本；false 表示没有可用缓存
// 预期：文件名 cover-<chatID>-<photoID>.jpg，photoID 可比较，取最大的那个
func (p *CoverProvider) diskCover(chatID int64) ([]byte, string, bool) {
	if p.cacheDir == "" {
		return nil, "", false
	}
	matches, err := filepath.Glob(filepath.Join(p.cacheDir, "cover-"+strconv.FormatInt(chatID, 10)+"-*.jpg"))
	if err != nil || len(matches) == 0 {
		return nil, "", false
	}
	bestFile, bestVer := "", int64(-1)
	for _, f := range matches {
		base := filepath.Base(f)
		// cover-<chatID>-<ver>.jpg → 取 <ver> 段
		ver := base[len("cover-"+strconv.FormatInt(chatID, 10)+"-") : len(base)-len(".jpg")]
		id, perr := strconv.ParseInt(ver, 10, 64)
		if perr != nil || id <= bestVer {
			continue
		}
		bestFile, bestVer = f, id
	}
	if bestFile == "" {
		return nil, "", false
	}
	data, err := os.ReadFile(bestFile)
	if err != nil {
		return nil, "", false
	}
	return data, strconv.FormatInt(bestVer, 10), true
}
