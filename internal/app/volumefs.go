package app

// 一个卷的读写后端装配：索引、存储、文件系统语义、文件管理。

import (
	"context"
	"time"

	"mytgfs/internal/config"
	"mytgfs/internal/datadir"
	"mytgfs/internal/fm"
	"mytgfs/internal/index"
	"mytgfs/internal/logging"
	"mytgfs/internal/queue"
	"mytgfs/internal/telegramstore"
	"mytgfs/internal/vfs"
)

// statusEventGap 是状态事件的最小推送间隔：进度按块触发，节流后前端每秒最多几次。
const statusEventGap = 400 * time.Millisecond

// VolumeFS 是一个卷的文件系统后端。
type VolumeFS struct {
	Volume config.Volume // 卷定义
	Name   string        // 解析后的卷名
	Index  *index.Index  // 本地索引
	Store  *telegramstore.Store
	FS     *vfs.FileSystem // 卷内文件系统后端
	Files  *fm.Manager     // 文件管理接口
}

// OpenVolume 为一个卷搭好读写后端。
//
// 输入：ctx；name 卷名（空取第一个）；api 已认证的 MTProto 客户端
// 输出：可直接使用的 VolumeFS
// 预期：
//   - 索引为空时先用 user 会话按频道历史重建，失败只告警不阻断
//   - 上传走串行队列；临时文件落 data/tmp，异常残留按时间清扫
//   - 重启后按账本补传上次未提交的文件
//   - 返回后索引归调用方所有，Close 由调用方执行
func (a *App) OpenVolume(ctx context.Context, name string, api telegramstore.API) (*VolumeFS, error) {
	vol, volName, ok := a.Volume(name)
	if !ok {
		return nil, ErrVolumeNotFound{Name: name}
	}
	idx, err := a.Index(volName)
	if err != nil {
		return nil, err
	}
	store := a.newStore(api, vol.ChatID)
	// 索引是缓存：空了就重建，否则面板与命令行都会看到空白目录
	if rerr := a.RebuildIndexIfEmpty(ctx, idx, vol.ChatID); rerr != nil {
		logging.Warn("log.volume_warn", rerr, volName)
	}

	fsys := vfs.New(ctx, idx, store, a.cfg.Mount.ReadChunk())
	// 批量写入互相抢 Telegram 发消息配额，串行提交
	fsys.SetQueue(queue.New(1, 3))
	fsys.SetTempDir(datadir.Path("tmp"))
	// 面板靠事件刷新状态：进度、提交、删除都推一次，节流避免高频刷屏
	if a.opts.Bus != nil {
		bus := a.opts.Bus
		fsys.SetOnChange(func() { bus.PublishEvery("status", statusEventGap) })
	}
	// 只补传本程序记下的记录，不动频道里别的内容
	if rerr := fsys.ResumePending(); rerr != nil {
		logging.Warn("log.volume_warn", rerr, volName)
	}
	return &VolumeFS{
		Volume: vol,
		Name:   volName,
		Index:  idx,
		Store:  store,
		FS:     fsys,
		Files:  fm.New(fsys),
	}, nil
}

// Close 关闭索引；连接池与上传队列由调用方负责。
func (v *VolumeFS) Close() error { return v.Index.Close() }
