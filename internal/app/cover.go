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
	"time"

	"mytgfs/internal/channel"
	"mytgfs/internal/push"
	"mytgfs/internal/telegramstore"
	"mytgfs/internal/volume"
)

// CoverStore 是卷级头像读取依赖（*telegramstore.Store 实现）。
type CoverStore interface {
	CoverVersion(ctx context.Context) (string, error)
	CoverImage(ctx context.Context) ([]byte, string, error)
	InvalidateCover()
	// Title 返回频道标题，作为面板上的显示名
	Title(ctx context.Context) (string, error)
	// SetTitle 改频道标题
	SetTitle(ctx context.Context, title string) error
	// BotUsername 返回 bot 用户名（补授权时用）
	BotUsername(ctx context.Context) (string, error)
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
	lookup func(name string) (coverVolume, bool)
	set    func(ctx context.Context, channelID int64, jpeg []byte) error
	// grant 用 user 会话把 bot 补成管理员（老卷缺 ChangeInfo 时用）
	grant    func(ctx context.Context, channelID, accessHash int64, botUsername string) error
	cacheDir string
	bus      *push.Hub

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
		// 标题由 bot 会话直接改（建卷时已授 ChangeInfo）；
		// 老卷缺这项权限，SetTitle 会自动补一次再重试
		grant: func(ctx context.Context, channelID, accessHash int64, botUsername string) error {
			return a.GrantChannelBot(ctx, channelID, accessHash, botUsername)
		},
		cacheDir: cacheDir,
		bus:      a.Bus(),
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

// Title 返回频道标题，面板用它作为卷的显示名。
//
// 输入：vol 卷名
// 输出：标题；卷未挂载或为空时返回空串
// 预期：只读元数据，缓存由 Store 负责；失败按空标题处理，面板回落到卷名
func (p *CoverProvider) Title(ctx context.Context, vol string) (string, error) {
	v, ok := p.lookup(vol)
	if !ok {
		return "", nil
	}
	return v.store.Title(ctx)
}

// SetTitle 改频道标题，成功后清本卷的频道元数据缓存。
//
// 输入：vol 卷名；title 新标题（去空白后不能为空）
// 输出：error；卷不存在时 ErrVolumeNotFound
// 预期：优先用带管理权限的 user 会话写（bot 改标题会被 CHAT_ADMIN_REQUIRED 拒绝）；
//
//	没配 user 会话时回退到 bot 会话，失败原因由上层报给面板
func (p *CoverProvider) SetTitle(ctx context.Context, vol, title string) error {
	v, ok := p.lookup(vol)
	if !ok {
		return ErrVolumeNotFound{Name: vol}
	}
	err := v.store.SetTitle(ctx, title)
	if channel.IsAdminRequired(err) {
		// 老卷建卷时只授了发消息/编辑/删除/置顶，缺 ChangeInfo：
		// 用 user 会话补一次权限，然后让 bot 重试
		if gerr := p.grantBot(ctx, v); gerr != nil {
			return gerr
		}
		err = v.store.SetTitle(ctx, title)
	}
	if err != nil {
		return err
	}
	v.store.InvalidateCover()
	p.InvalidateVolume(vol)
	if p.bus != nil {
		p.bus.Publish(push.TopicStatus)
	}
	return nil
}

// grantBot 用 user 会话把 bot 的频道权限补齐。
//
// 输入：v 目标卷（含 store 与频道号）
// 输出：error；bot 用户名读不到或补授权失败时返回
// 预期：只在 bot 改标题被 CHAT_ADMIN_REQUIRED 拒绝时调用，一次性修复老卷
func (p *CoverProvider) grantBot(ctx context.Context, v coverVolume) error {
	name, err := v.store.BotUsername(ctx)
	if err != nil {
		return err
	}
	return p.grant(ctx, v.chatID, 0, name)
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

	// 冷启动/缓存失效后：磁盘上有上一张就直接用，不阻塞面板；
	// 版本异步核对，外部换头像后被纠正并推送
	if p.cacheDir != "" {
		if data, version, found := p.diskCover(cv.chatID); found {
			p.remember(cv.chatID, version, data)
			p.verifyLater(cv, version)
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

// PurgeChat 删卷时清掉该频道的全部头像缓存（内存 + 磁盘）。
//
// 输入：chatID 频道 id（非 -100... 形式）
// 输出：无
// 预期：卷已删，缓存文件成为孤儿，必须清掉；磁盘文件按 chatID 前缀匹配
func (p *CoverProvider) PurgeChat(chatID int64) {
	p.mu.Lock()
	delete(p.cache, chatID)
	p.mu.Unlock()
	if p.cacheDir == "" {
		return
	}
	stale, _ := filepath.Glob(filepath.Join(p.cacheDir, p.fileName(chatID, "*")))
	for _, f := range stale {
		_ = os.Remove(f)
	}
}

// SetCover 写入频道头像并让本卷的缓存立即换成新字节。
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
	// 新头像立刻进缓存：现读新 photo id，面板下一次刷新马上显示新图，
	// 不给磁盘里的旧版本文件任何复辟机会
	version, verr := v.store.CoverVersion(ctx)
	if verr == nil && version != "" {
		p.remember(v.chatID, version, data)
	} else {
		p.drop(v.chatID)
	}
	return nil
}

// verifyLater 异步核对磁盘缓存的版本：外部换头像后被纠正并推送面板。
func (p *CoverProvider) verifyLater(cv coverVolume, cachedVersion string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if !p.verifyDisk(ctx, cv, cachedVersion) {
			return
		}
		if p.bus != nil {
			p.bus.Publish(push.TopicStatus)
		}
	}()
}

// verifyDisk 核对缓存版本，外部换了头像就回源新字节并更新缓存。
//
// 输入：cv 卷信息；cachedVersion 当前缓存的版本
// 输出：true 表示缓存被更新（需要推送面板刷新）
// 预期：版本一致时零额外下载；Telegram 不可达时保持旧缓存
func (p *CoverProvider) verifyDisk(ctx context.Context, cv coverVolume, cachedVersion string) bool {
	version, err := cv.store.CoverVersion(ctx)
	if err != nil || version == cachedVersion || version == "" {
		return false
	}
	data, newVersion, err := cv.store.CoverImage(ctx)
	if err != nil || newVersion == "" {
		return false
	}
	p.remember(cv.chatID, newVersion, data)
	return true
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
