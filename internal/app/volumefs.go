package app

// 一个卷的读写后端装配：索引、存储、文件系统语义、文件管理。

import (
	"context"
	"os"
	"strconv"

	"mytgfs/internal/config"
	"mytgfs/internal/datadir"
	"mytgfs/internal/fm"
	"mytgfs/internal/index"
	"mytgfs/internal/logging"
	"mytgfs/internal/push"
	"mytgfs/internal/queue"
	"mytgfs/internal/telegramstore"
	"mytgfs/internal/vfs"
)

// Events 返回写操作的推送通道。
//
// 输入：无
// 输出：push.Events；装配时没给 Hub 时是 nil（空实现）
// 预期：所有构造卷的地方都从这里取，忘接推送会编译不过
func (a *App) Events() push.Events {
	if a.opts.Push == nil {
		return nil
	}
	return a.opts.Push
}

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

	fsys := vfs.New(ctx, idx, store, a.cfg.Mount.ReadChunk(), a.Events())
	fsys.SetVolume(volName)
	// 所有改动频道的操作先落库再执行：断电/断网后重启继续
	if _, qerr := fsys.AttachQueue(ctx); qerr != nil {
		_ = idx.Close()
		return nil, qerr
	}
	fsys.SetTempDir(datadir.Path("tmp"))
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

// Snapshot 是一个卷在面板上的状态。
type Snapshot struct {
	Name      string         `json:"name"`
	Entries   int            `json:"entries"`
	Used      int64          `json:"used"`
	Transfers []vfs.Transfer `json:"-"`
	Queue     []queue.Item   `json:"-"`
}

// Snapshot 汇总卷统计、下载进度与任务队列。
//
// 输入：ctx
// 输出：卷名、根条目数、占用字节、带卷名前缀的下载进度、任务快照
// 预期：索引读失败时返回错误，由调用方决定是整卷报错还是跳过
func (v *VolumeFS) Snapshot(ctx context.Context) (Snapshot, error) {
	children, err := v.Index.Children(ctx, 0)
	if err != nil {
		return Snapshot{}, err
	}
	used, _ := v.Index.Used(ctx)
	snap := Snapshot{Name: v.Name, Entries: len(children), Used: used}
	if v.FS == nil {
		return snap, nil
	}
	for _, t := range v.FS.Transfers() {
		t.Path = v.Name + ":" + t.Path
		snap.Transfers = append(snap.Transfers, t)
	}
	if q := v.FS.Queue(); q != nil {
		snap.Queue = q.Snapshot()
	}
	return snap, nil
}

// VideoThumb 返回指定路径文件的 Telegram 缩略图。
//
// 输入：ctx；path 卷内路径
// 输出：缩略图字节；版本（消息 ID）；error
// 预期：目录或不存在返回 os.ErrNotExist，交给 handler 映射 404
func (v *VolumeFS) VideoThumb(ctx context.Context, path string) ([]byte, string, error) {
	n, ok, err := v.Index.NodeByPath(ctx, path)
	if err != nil {
		return nil, "", err
	}
	if !ok || n.IsDir {
		return nil, "", os.ErrNotExist
	}
	data, terr := v.Store.Thumb(ctx, n.ID)
	return data, strconv.Itoa(n.ID), terr
}

// ClearQueue 清空本卷已结束的任务记录。
//
// 输入：ctx
// 输出：删除条数
// 预期：只删 done/failed；排队与执行中的任务不受影响，队列未接线时返回 0
func (v *VolumeFS) ClearQueue(ctx context.Context) (int64, error) {
	if v.FS == nil {
		return 0, nil
	}
	q := v.FS.Queue()
	if q == nil {
		return 0, nil
	}
	return q.ClearFinished(ctx)
}

// Close 先停队列再关索引；在跑任务被取消后等下次启动重排。
func (v *VolumeFS) Close() error {
	v.FS.Close()
	return v.Index.Close()
}
