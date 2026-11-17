package app

// 删卷：删频道 → 移出配置 → 清缓存与索引。
//
// CLI 的 volume delete、`mytgfs web` 面板、桌面/安卓面板都走这一处实现，
// 不在各自那边再拼一遍流程。运行时账本（停同步、摘注册表）依赖各自的
// 装配根，由调用方在删卷成功后自行对齐。

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/gotd/td/tg"

	"mytgfs/internal/channel"
	"mytgfs/internal/config"
	"mytgfs/internal/datadir"
	"mytgfs/internal/logging"
	"mytgfs/internal/volume"
)

// VolumeDeleteResult 描述一次删卷的结果。
type VolumeDeleteResult struct {
	// Name 是配置里实际匹配到的卷名，大小写可能与入参不同
	Name string `json:"name"`
	// ChannelID 是被处理过的频道号
	ChannelID int64 `json:"channel_id"`
	// ChannelDeleted 为 false 表示频道本来就不存在，只清理了本地引用
	ChannelDeleted bool `json:"channel_deleted"`
}

// DeleteVolume 永久删除一个卷。
//
// 输入：ctx；name 卷名（大小写不敏感，允许首尾空白）
// 输出：删除结果；卷不存在返回 ErrVolumeNotFound，缺 user 会话返回 ErrUserSessionMissing
// 预期：
//   - 频道已不存在视为删除成功（ChannelDeleted=false），继续清理本地引用
//   - 其它频道操作错误直接返回，配置与缓存保持原样
//   - 成功后：移除配置条目 → 删缩略图目录 → 删封面缓存 → 删索引文件
func (a *App) DeleteVolume(ctx context.Context, name string) (VolumeDeleteResult, error) {
	target, ok := a.findConfiguredVolume(name)
	if !ok {
		return VolumeDeleteResult{}, ErrVolumeNotFound{Name: strings.TrimSpace(name)}
	}

	res := VolumeDeleteResult{Name: target.Name, ChannelID: target.ChatID}
	// 频道已不存在时不再重试：目标就是把这条引用清掉
	err := a.withUserAPI(ctx, func(ctx context.Context, api *tg.Client) error {
		ch, rerr := channel.Resolve(ctx, api, target.ChatID)
		if rerr != nil {
			if channel.IsStale(rerr) {
				return nil
			}
			return rerr
		}
		if derr := channel.Delete(ctx, api, ch); derr != nil {
			return derr
		}
		res.ChannelDeleted = true
		return nil
	})
	if err != nil {
		return VolumeDeleteResult{}, err
	}

	// 频道这一侧已有定论，下面的本地清理尽量做完，避免留下半套配置
	if merr := a.MutateConfig(func(p string) error {
		return config.RemoveVolume(p, target.Name)
	}); merr != nil {
		return VolumeDeleteResult{}, merr
	}
	a.purgeVolumeCaches(target)
	return res, nil
}

// findConfiguredVolume 只在显式配置的卷里查找，不套用无 [[volumes]] 时的隐式卷。
//
// 输入：卷名
// 输出：匹配到的卷定义；false 表示配置里没有
// 预期：大小写不敏感，忽略首尾空白
func (a *App) findConfiguredVolume(name string) (config.Volume, bool) {
	name = strings.TrimSpace(name)
	vols := a.Config().Volumes
	i := slices.IndexFunc(vols, func(v config.Volume) bool {
		return strings.EqualFold(v.Name, name)
	})
	if i < 0 {
		return config.Volume{}, false
	}
	return vols[i], true
}

// purgeVolumeCaches 删掉这个卷的本地派生物：缩略图、封面、索引。
//
// 输入：卷定义
// 输出：无
// 预期：缓存是可再生的，删除失败只记日志不中断删卷
func (a *App) purgeVolumeCaches(v config.Volume) {
	thumbs := datadir.Path("thumbs")
	// 缩略图按卷分目录
	if err := os.RemoveAll(filepath.Join(thumbs, v.Name)); err != nil && !os.IsNotExist(err) {
		logging.Warn("log.volume_cache_purge_failed", err, v.Name)
	}
	// 封面就是频道头像，按 cover-<chatID>-<photoID>.jpg 散落在 thumbs 根目录
	chatID := volume.NormalizeChatID(v.ChatID)
	stale, _ := filepath.Glob(filepath.Join(thumbs, "cover-"+strconv.FormatInt(chatID, 10)+"-*.jpg"))
	for _, f := range stale {
		if err := os.Remove(f); err != nil && !os.IsNotExist(err) {
			logging.Warn("log.volume_cache_purge_failed", err, f)
		}
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		p := datadir.Path("index-" + v.Name + ".db" + suffix)
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			logging.Warn("log.volume_cache_purge_failed", err, p)
		}
	}
}
