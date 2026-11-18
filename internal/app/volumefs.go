package app

// 一个卷的读写后端装配：索引、存储、文件系统语义、文件管理。

import (
	"context"
	"log/slog"
	"os"
	"strconv"
	"sync"

	"mytgfs/internal/config"
	"mytgfs/internal/datadir"
	"mytgfs/internal/fm"
	"mytgfs/internal/i18n"
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

	// peerMu/peers 缓存跨卷复制用到的源卷索引：
	// 每个复制任务都新开一条 SQLite 连接，会与源卷自己的连接抢锁并重复开销
	peerMu sync.Mutex
	peers  map[string]*index.Index
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
	store := a.newStore(api, vol.ChatID, idx)
	// 索引按卷名命名：换过频道的卷必须先丢掉旧频道的数据，再决定是否重建
	if aerr := a.adoptChannel(ctx, idx, vol.ChatID); aerr != nil {
		logging.Warn("log.volume_warn", aerr, volName)
	}
	// 索引是缓存：空了就重建，否则面板与命令行都会看到空白目录
	if rerr := a.RebuildIndexIfEmpty(ctx, idx, vol.ChatID); rerr != nil {
		logging.Warn("log.volume_warn", rerr, volName)
	}

	fsys := vfs.New(ctx, idx, store, a.Config().Mount.ReadChunk(), a.Events())
	fsys.SetVolume(volName)
	// 排队的跨卷复制任务要按卷名解析源卷：注入这一层，重启后也能重放
	v := &VolumeFS{
		Volume: vol,
		Name:   volName,
		Index:  idx,
		Store:  store,
		FS:     fsys,
		Files:  fm.New(fsys),
		peers:  map[string]*index.Index{},
	}
	fsys.SetCopySource(func(name string) (vfs.Peer, error) {
		if name == volName {
			return vfs.Peer{Index: idx, Store: store}, nil
		}
		pv, pn, ok := a.Volume(name)
		if !ok {
			return vfs.Peer{}, ErrVolumeNotFound{Name: name}
		}
		// 源卷索引复用一条连接：每个复制任务都 open/close 会与源卷自己的
		// 连接反复抢锁，同一卷连续复制时开销翻倍
		pidx, perr := a.peerIndex(v, pn)
		if perr != nil {
			return vfs.Peer{}, perr
		}
		return vfs.Peer{
			Index: pidx,
			Store: a.newStore(api, pv.ChatID, pidx),
		}, nil
	})
	// 所有改动频道的操作先落库再执行：断电/断网后重启继续
	if _, qerr := fsys.AttachQueue(ctx); qerr != nil {
		_ = idx.Close()
		return nil, qerr
	}
	fsys.SetTempDir(datadir.Path("tmp"))
	// 只补传本程序记下的记录，不动频道里别的内容。
	// 放到后台跑：大文件要整份算 sha256，不能把打开卷（首屏）卡住
	go func() {
		if rerr := fsys.ResumePending(); rerr != nil {
			logging.Warn("log.pending_resume_failed", rerr, volName)
		}
	}()
	return v, nil
}

// peerIndex 返回跨卷复制用的源卷索引（按卷名缓存）。
//
// 输入：v 持有缓存的卷；name 源卷名
// 输出：已打开的索引；error
// 预期：同一源卷只开一条连接，VolumeFS.Close 时统一关闭
func (a *App) peerIndex(v *VolumeFS, name string) (*index.Index, error) {
	v.peerMu.Lock()
	defer v.peerMu.Unlock()
	if idx, ok := v.peers[name]; ok {
		return idx, nil
	}
	idx, err := a.Index(name)
	if err != nil {
		return nil, err
	}
	if v.peers == nil {
		v.peers = map[string]*index.Index{}
	}
	v.peers[name] = idx
	return idx, nil
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
	used, uerr := v.Index.Used(ctx)
	if uerr != nil {
		return Snapshot{}, uerr
	}
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
	// 索引里已有 stripped 展开：零网络，直接出图
	if len(n.Thumb) > 0 {
		return n.Thumb, strconv.Itoa(n.ID), nil
	}
	data, terr := v.Store.Thumb(ctx, n.ID)
	if terr == nil && len(data) > 0 {
		// 回源成功顺手入库：下次也零请求
		n.Thumb = data
		if err := v.Index.PutNode(ctx, n); err != nil {
			slog.Warn(i18n.T("log.thumb_index_failed"), "path", path, "err", err)
		}
	}
	return data, strconv.Itoa(n.ID), terr
}

// ClearQueue 清空本卷已结束的任务记录。
//
// 输入：ctx
// 输出：删除条数
// 预期：只删 done/failed/cancelled；排队与执行中的任务不受影响，队列未接线时返回 0
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

// CancelQueue 取消本卷的一条排队中或执行中的任务。
//
// 输入：ctx；id 任务号
// 输出：queue.ErrJobNotFound / queue.ErrJobFinished；队列未接线时返回 ErrJobNotFound
// 预期：执行中的上传被打断后同时丢弃临时文件与 pending 账本，重启不会自己再传
func (v *VolumeFS) CancelQueue(ctx context.Context, id int64) error {
	if v.FS == nil {
		return queue.ErrJobNotFound
	}
	q := v.FS.Queue()
	if q == nil {
		return queue.ErrJobNotFound
	}
	return q.Cancel(ctx, id)
}

// Close 先停队列再关索引；在跑任务被取消后等下次启动重排。
func (v *VolumeFS) Close() error {
	v.FS.Close()
	v.peerMu.Lock()
	peers := v.peers
	v.peers = nil
	v.peerMu.Unlock()
	for _, idx := range peers {
		_ = idx.Close()
	}
	return v.Index.Close()
}
