package vfs

// Package vfs 是 FUSE 适配层。
//
// 数据流：元数据读本地 SQLite 索引，文件内容按需走 telegramstore 随机读；
// 写入先落磁盘临时文件（不占内存），Release 时上传 Telegram 并写入索引。
// 支持：读、写、截断、删除、重命名；目录由 path 推导，不单独存储。
// 不支持：硬链接、软链接。

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/golang-lru/v2"
	"github.com/winfsp/cgofuse/fuse"

	"mytgfs/internal/index"
	tgsync "mytgfs/internal/sync"
	"mytgfs/internal/telegramstore"
)

// fillStat 补齐 WinFsp 需要的 Stat_t 字段。
func fillStat(stat *fuse.Stat_t, mode uint32, size int64, ino uint64) {
	uid, gid, _ := fuse.Getcontext()
	stat.Mode = mode
	stat.Nlink = 1
	if mode&fuse.S_IFDIR != 0 {
		stat.Nlink = 2
	}
	stat.Uid = uid
	stat.Gid = gid
	stat.Ino = ino
	stat.Size = size
	now := fuse.Now()
	stat.Atim = now
	stat.Mtim = now
	stat.Ctim = now
	stat.Birthtim = now
}

// pathIno 为路径生成稳定的 inode 号。
func pathIno(p string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(p))
	return h.Sum64()
}

func debugf(format string, args ...any) {
	slog.Debug(fmt.Sprintf(format, args...))
}

// FS 是挂载的文件系统。
type FS struct {
	fuse.FileSystemBase
	idx    *index.Index
	store  Store
	ctx    context.Context
	tmpDir string

	mu      sync.Mutex
	handles map[uint64]*openFile
	pending map[string]*openFile
	next    uint64
	notify  func(p string, action uint32)

	// opMu 串行化「改索引 + 改远端」的复合操作。
	// 输入：无；输出：无；预期：Release 提交与 Rename/Unlink 不会交错执行，
	// 否则改名可能拿到提交前的旧消息号（Telegram 返回 MESSAGE_ID_INVALID）
	opMu sync.Mutex

	// progMu 保护下载进度表；进度只用于展示，不参与任何逻辑
	progMu sync.Mutex
	prog   map[string]*Transfer

	// rc 是预读窗口的 LRU 缓存（hashicorp/golang-lru 自带淘汰与并发保护）
	rc     *lru.Cache[string, *readCache]
	rcOnce sync.Once

	// retryBase 是上传重试的首次等待时长，之后逐次翻倍；测试置 0 跳过等待
	retryBase time.Duration
}

// 预读窗口参数。
const (
	// readWindow 是缓存块大小，与下载器的 partSize 对齐
	readWindow = 1 << 20
	// probeSpan 是文件首次访问时的拉取跨度：缩略图/属性只要文件头，
	// 不值得为了看一眼就把整页拉下来
	probeSpan = 1 << 20
	// fetchSpan 是第二次及以后每次拉取的跨度：一次 getFile 覆盖多块并行下载，
	// 避免按 1MB 反复发起请求被单次往返开销拖死
	fetchSpan = 8 << 20
	// maxWindowsPerFile 是单文件最多缓存的块数（约 10MB）
	maxWindowsPerFile = 10
	// readAheadMaxFiles 是同时缓存的文件数上限，内存上限约 30MB
	readAheadMaxFiles = 3
)

// readCache 是一个文件已下载的若干块。
type readCache struct {
	// windows 以块起点为键，值恰为 [start, start+len) 的字节
	windows map[int64][]byte
	// order 记录块插入顺序，超出上限时淘汰最早的一块
	order []int64
	// primed 为真表示已经探测过文件头，后续按大跨度拉取
	primed bool
}

// cacheRead 命中已缓存的块时直接返回，否则拉一段再返回。
//
// 输入：file 索引条目；ofst 起始偏移；length 需要的长度
// 输出：字节切片；是否命中缓存；error
// 预期：
//   - 首次访问按 probeSpan、之后按 fetchSpan 对齐拉取，拉到的数据拆成
//     readWindow 大小的块，任意偏移命中即可用
//   - 请求跨段时返回未命中，由调用方按精确范围再读一次
//   - 拉取失败返回 error，由调用方报 EIO
func (f *FS) cacheRead(file index.File, ofst int64, length int) ([]byte, bool, error) {
	f.rcOnce.Do(func() {
		c, err := lru.New[string, *readCache](readAheadMaxFiles)
		if err != nil {
			// readAheadMaxFiles 固定为正数，构造失败不可能发生
			panic(err)
		}
		f.rc = c
	})

	end := ofst + int64(length)
	block := ofst / readWindow * readWindow
	c, cached := f.rc.Get(file.Path)
	if cached {
		if data, ok := c.windows[block]; ok && end <= block+int64(len(data)) {
			return data[ofst-block : end-block], true, nil
		}
	}

	span := int64(fetchSpan)
	if !cached || !c.primed {
		span = probeSpan
	}
	start := ofst / span * span
	want := span
	if start+want > file.Size {
		want = file.Size - start
	}
	if want <= 0 {
		return nil, false, nil
	}
	// 请求可能跨出本段：把本段拉到能覆盖请求为止
	if end-start > want && end-start <= fetchSpan {
		want = end - start
	}

	t0 := time.Now()
	data, nfile, err := f.readRangeWithRefresh(file, start, int(want), func(done int64) {
		// downloader 的进度是文件内绝对偏移，直接透传
		f.trackRead(file.Path, done, file.Size)
	})
	if err != nil {
		debugf("vfs: 预读失败 %s start=%d len=%d: %v", shortPath(file.Path), start, want, err)
		return nil, false, err
	}
	debugf("vfs: 拉取 %s start=%d len=%d 耗时=%s 命中=否", shortPath(file.Path), start, len(data), time.Since(t0).Round(time.Millisecond))
	if end > start+int64(len(data)) {
		return nil, false, nil
	}
	if nfile.DocumentID == file.DocumentID {
		f.putSpan(file.Path, start, data)
	}
	return data[ofst-start : end-start], true, nil
}

// putSpan 把一段数据按 readWindow 拆块放进缓存，超出块数上限时淘汰最早的块。
//
// 输入：path 路径；start 段起点（按 readWindow 对齐）；data 段内容
// 输出：无
// 预期：并发安全；同一块重复写入时覆盖；标记该文件已探测过文件头
func (f *FS) putSpan(path string, start int64, data []byte) {
	c, ok := f.rc.Get(path)
	if !ok {
		c = &readCache{windows: map[int64][]byte{}}
	}
	c.primed = true
	for off := 0; off < len(data); off += readWindow {
		blockStart := start + int64(off)
		end := off + readWindow
		if end > len(data) {
			end = len(data)
		}
		if _, exists := c.windows[blockStart]; !exists {
			c.order = append(c.order, blockStart)
		}
		c.windows[blockStart] = data[off:end]
	}
	for len(c.order) > maxWindowsPerFile {
		old := c.order[0]
		c.order = c.order[1:]
		delete(c.windows, old)
	}
	f.rc.Add(path, c)
}

// dropCache 清掉某个路径的预读窗口。
//
// 输入：p 路径
// 输出：无
// 预期：写、删、改名后调用，避免读到旧内容
func (f *FS) dropCache(p string) {
	f.rcOnce.Do(func() {
		c, _ := lru.New[string, *readCache](readAheadMaxFiles)
		f.rc = c
	})
	f.rc.Remove(p)
}

// Transfer 是一个正在下载的文件的进度。
type Transfer struct {
	Path  string `json:"path"`
	Done  int64  `json:"done"`
	Total int64  `json:"total"`
}

// Transfers 返回当前有下载活动的文件快照。
//
// 输入：无
// 输出：按路径排序的进度列表；完成的条目会被移除
// 预期：并发安全；只读，不阻塞读路径
func (f *FS) Transfers() []Transfer {
	f.progMu.Lock()
	defer f.progMu.Unlock()
	out := make([]Transfer, 0, len(f.prog))
	for _, t := range f.prog {
		out = append(out, *t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// trackRead 记录一次远端读取后的进度，并在跨过 10% 时打一行日志。
//
// 输入：p 路径；done 已读字节；total 文件总大小
// 输出：无
// 预期：读满时从进度表移除；日志只在整十百分比时输出，避免刷屏
func (f *FS) trackRead(p string, done, total int64) {
	if total <= 0 {
		return
	}
	if done > total {
		done = total
	}
	f.progMu.Lock()
	if f.prog == nil {
		f.prog = make(map[string]*Transfer)
	}
	prev := f.prog[p]
	prevPct := 0
	if prev != nil && prev.Total > 0 {
		prevPct = int(prev.Done * 100 / prev.Total)
	}
	cur := int(done * 100 / total)
	if done >= total {
		delete(f.prog, p)
	} else {
		f.prog[p] = &Transfer{Path: p, Done: done, Total: total}
	}
	f.progMu.Unlock()

	if cur/10 > prevPct/10 {
		debugf("vfs: 下载 %s %d%% (%d/%d 字节)", shortPath(p), cur, done, total)
	}
}

// clearProgress 移除某个路径的下载进度条目。
//
// 输入：p 路径
// 输出：无
// 预期：关闭文件或读到末尾后调用，避免面板残留已结束的下载
func (f *FS) clearProgress(p string) {
	f.progMu.Lock()
	delete(f.prog, p)
	f.progMu.Unlock()
}

// openFile 是一个待提交的节点。
type openFile struct {
	path  string
	tmp   *os.File
	dirty bool
	size  int64
	// baseMsg 是写打开时索引里的消息号；提交前用它检测别的端是否已经改过同一个路径。
	// 0 表示打开时文件还不存在。
	baseMsg int
	// refs 是同时打开该节点的 handle 数；归零才允许提交并清理 pending。
	refs int
	// committing 为真表示有 goroutine 正在上传该节点；此时节点仍留在
	// pending 里，保证名字和内容对读操作依然可见。
	committing bool
	// commitDone 在提交结束时关闭；写打开撞上提交中的节点时等它结束再重试。
	commitDone chan struct{}
	// done 为真表示已提交完成，后续 Release 不得重复上传。
	done bool
}

// New 创建文件系统。
//
// 输入：ctx；idx 索引；store 存储；tmpDir 写入临时目录
// 输出：*FS
// 预期：tmpDir 为空时使用系统临时目录；顺带清掉 7 天前的上传临时文件
func New(ctx context.Context, idx *index.Index, store Store, tmpDir string) *FS {
	if tmpDir == "" {
		tmpDir = os.TempDir()
	}
	cleanStaleTemps(tmpDir)
	return &FS{
		idx:       idx,
		store:     store,
		ctx:       ctx,
		tmpDir:    tmpDir,
		handles:   map[uint64]*openFile{},
		pending:   map[string]*openFile{},
		retryBase: time.Second,
	}
}

// cleanStaleTemps 删除长时间未使用的上传临时文件。
//
// 输入：临时目录
// 输出：无
// 预期：只删 7 天前的 `.mytgfs-upload-*`，正在进行的写入不会被误删
func cleanStaleTemps(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-7 * 24 * time.Hour)
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), ".mytgfs-upload-") {
			continue
		}
		info, ierr := e.Info()
		if ierr != nil || info.ModTime().After(cutoff) {
			continue
		}
		_ = os.Remove(filepath.Join(dir, e.Name()))
	}
}

// Close 释放待上传节点。
//
// 输入：无
// 输出：无
// 预期：卸载时调用；未提交的内容不删除，日志给出临时文件路径供抢救
func (f *FS) Close() {
	f.mu.Lock()
	pending := make([]*openFile, 0, len(f.pending))
	for p, h := range f.pending {
		pending = append(pending, h)
		delete(f.pending, p)
	}
	f.handles = map[uint64]*openFile{}
	f.mu.Unlock()
	for _, h := range pending {
		if h.tmp == nil {
			continue
		}
		_ = h.tmp.Close()
		if _, err := os.Stat(h.tmp.Name()); err == nil {
			slog.Warn("vfs: 卸载时仍有未上传的内容",
				"path", h.path, "temp", h.tmp.Name())
		}
	}
}

// SetNotify 设置目录变更通知回调，由 Mount 在挂载前注入。
func (f *FS) SetNotify(fn func(p string, action uint32)) {
	f.mu.Lock()
	f.notify = fn
	f.mu.Unlock()
}

// notifyChange 通知资源管理器某个路径发生变化。
func (f *FS) notifyChange(p string, action uint32) {
	f.mu.Lock()
	fn := f.notify
	f.mu.Unlock()
	if fn != nil {
		fn(p, action)
	}
}

// NotifyPaths 把远端增量同步的结果映射成挂载点刷新事件。
//
// 输入：changed 新增/编辑的路径；removed 删除的路径
// 输出：无
// 预期：changed 用 CREATE 让资源管理器重新取属性与内容；
//
//	removed 用 UNLINK 让对应节点消失；无路径时不做任何事
func (f *FS) NotifyPaths(changed, removed []string) {
	for _, p := range removed {
		f.notifyChange(p, fuse.NOTIFY_UNLINK)
	}
	for _, p := range changed {
		f.notifyChange(p, fuse.NOTIFY_CREATE)
	}
}

// newHandle 分配一个文件句柄并给节点加引用。
//
// 输入：h 节点（nil 表示无缓冲的只读句柄）
// 输出：句柄号
// 预期：调用方必须已持有 f.mu；h 非 nil 时 refs 加一
func (f *FS) newHandle(h *openFile) uint64 {
	f.next++
	hid := f.next
	f.handles[hid] = h
	if h != nil {
		h.refs++
	}
	return hid
}

// Getattr 取文件属性。
func (f *FS) Getattr(p string, stat *fuse.Stat_t, fh uint64) int {
	p = clean(p)
	if p == "/" {
		fillStat(stat, fuse.S_IFDIR|0o777, 0, pathIno(p))
		return 0
	}
	// 正在写入、尚未上传的文件
	f.mu.Lock()
	if h, ok := f.pending[p]; ok {
		size := h.size
		f.mu.Unlock()
		fillStat(stat, fuse.S_IFREG|0o666, size, pathIno(p))
		return 0
	}
	for _, h := range f.handles {
		if h.path == p && h.tmp != nil {
			size := h.size
			f.mu.Unlock()
			fillStat(stat, fuse.S_IFREG|0o666, size, pathIno(p))
			return 0
		}
	}
	f.mu.Unlock()
	file, ok, err := f.idx.Get(f.ctx, p)
	if err != nil {
		return -fuse.EIO
	}
	if ok {
		// 输入：索引命中；输出：索引里的字节数
		// 预期：调用方看到的就是已提交大小，配日志便于排查「写完读回为 0」
		debugf("vfs: Getattr %s size=%d src=index", p, file.Size)
		fillStat(stat, fuse.S_IFREG|0o666, file.Size, pathIno(p))
		return 0
	}
	if f.isDir(p) {
		fillStat(stat, fuse.S_IFDIR|0o777, 0, pathIno(p))
		return 0
	}
	return -fuse.ENOENT
}

// Fgetattr 用句柄取属性。
//
// 输入：路径 p、统计结构 stat、句柄 fh
// 输出：0 或负 errno
// 预期：命中句柄时用句柄里的实时 size；否则退回按路径取，供 PowerShell 提供程序使用
func (f *FS) Fgetattr(p string, stat *fuse.Stat_t, fh uint64) int {
	f.mu.Lock()
	h := f.handles[fh]
	if h != nil {
		size := h.size
		f.mu.Unlock()
		// 输入：句柄命中；输出：句柄记录的字节数
		// 预期：写句柄返回实时大小，读句柄返回打开时的索引大小
		debugf("vfs: Fgetattr %s size=%d src=handle fh=%d tmp=%v", p, size, fh, h.tmp != nil)
		fillStat(stat, fuse.S_IFREG|0o666, size, pathIno(clean(p)))
		return 0
	}
	f.mu.Unlock()
	return f.Getattr(p, stat, fh)
}

// Flush 刷新句柄；内容在 Release 统一提交，这里直接成功。
func (f *FS) Flush(p string, fh uint64) int { return 0 }

// Fsync 同步句柄；内容在 Release 统一提交，这里直接成功。
func (f *FS) Fsync(p string, datasync bool, fh uint64) int { return 0 }

// Utimens 更新时间戳；不持久化时间戳，直接成功。
func (f *FS) Utimens(p string, tmsp []fuse.Timespec) int { return 0 }

// Chmod 改权限；虚拟盘统一 0666/0777，直接成功。
func (f *FS) Chmod(p string, mode uint32) int { return 0 }

// Chown 改属主；虚拟盘不区分属主，直接成功。
func (f *FS) Chown(p string, uid uint32, gid uint32) int { return 0 }

// Opendir 打开目录（资源管理器必须先调它）。
func (f *FS) Opendir(p string) (int, uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.next++
	return 0, f.next
}

// Releasedir 关闭目录。
func (f *FS) Releasedir(p string, fh uint64) int { return 0 }

// Readdir 列目录。
func (f *FS) Readdir(p string, fill func(name string, stat *fuse.Stat_t, ofst int64) bool, ofst int64, fh uint64) int {
	p = clean(p)
	entries, err := f.idx.List(f.ctx, p)
	if err != nil {
		return -fuse.EIO
	}
	fill(".", nil, 0)
	fill("..", nil, 0)
	for _, e := range entries {
		full := path.Join(p, e.Name)
		st := &fuse.Stat_t{}
		if e.IsDir {
			fillStat(st, fuse.S_IFDIR|0o777, 0, pathIno(full))
		} else {
			var size int64
			if e.File != nil {
				size = e.File.Size
			}
			fillStat(st, fuse.S_IFREG|0o666, size, pathIno(full))
		}
		if !fill(e.Name, st, 0) {
			return 0
		}
	}
	f.mu.Lock()
	for pendingPath, h := range f.pending {
		if path.Dir(pendingPath) != p {
			continue
		}
		st := &fuse.Stat_t{}
		fillStat(st, fuse.S_IFREG|0o666, h.size, pathIno(pendingPath))
		fill(path.Base(pendingPath), st, 0)
	}
	f.mu.Unlock()
	return 0
}

// Mknod 创建待上传文件（memfs 模式：先建节点，再由 Open 取 handle）。
func (f *FS) Mknod(p string, mode uint32, dev uint64) int {
	debugf("vfs: Mknod %s mode=%o", p, mode)
	p = clean(p)
	f.mu.Lock()
	if _, ok := f.pending[p]; ok {
		// 已存在待上传节点，复用，避免覆盖未提交内容
		f.mu.Unlock()
		return 0
	}
	f.mu.Unlock()
	tmp, err := os.CreateTemp(f.tmpDir, ".mytgfs-upload-*")
	if err != nil {
		debugf("vfs: Mknod %s temp error: %v", p, err)
		return -fuse.EIO
	}
	f.mu.Lock()
	if _, ok := f.pending[p]; ok {
		f.mu.Unlock()
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return 0
	}
	f.pending[p] = &openFile{path: p, tmp: tmp, dirty: true}
	f.mu.Unlock()
	f.notifyChange(p, fuse.NOTIFY_CREATE)
	return 0
}

// Create 交给 Mknod + Open 处理，返回 ENOSYS 让 cgofuse 走标准路径。
// （memfs 官方示例也不实现 Create）

// Create 新建文件（Windows 的 CreateFile 直接走这里）。
//
// 输入：路径 p、flags、mode
// 输出：0 与句柄
// 预期：建磁盘临时文件并登记 pending，Release 时上传到 Telegram
func (f *FS) Create(p string, flags int, mode uint32) (int, uint64) {
	debugf("vfs: Create %s flags=%d", p, flags)
	p = clean(p)
	f.mu.Lock()
	if h, ok := f.pending[p]; ok {
		hid := f.newHandle(h)
		f.mu.Unlock()
		return 0, hid
	}
	f.mu.Unlock()
	tmp, err := os.CreateTemp(f.tmpDir, ".mytgfs-upload-*")
	if err != nil {
		debugf("vfs: Create %s temp error: %v", p, err)
		return -fuse.EIO, 0
	}
	h := &openFile{path: p, tmp: tmp, dirty: true}
	f.mu.Lock()
	f.pending[p] = h
	hid := f.newHandle(h)
	f.mu.Unlock()
	f.notifyChange(p, fuse.NOTIFY_CREATE)
	return 0, hid
}

// Open 打开文件。
//
// 输入：路径 p、flags
// 输出：0 与句柄（永不返回 ENOENT）
// 预期：命中待上传节点用同一个节点加引用；命中索引给只读句柄；
//
//	都未命中时给空句柄，让上层自己拿到 EBADF，而不是在这里抛 ENOENT
func (f *FS) Open(p string, flags int) (int, uint64) {
	debugf("vfs: Open %s flags=%d", p, flags)
	p = clean(p)
	f.mu.Lock()
	if h, ok := f.pending[p]; ok {
		// 提交中的节点不能挂写句柄：它马上要关闭并删除临时文件。
		// 等提交结束再重开，避免读到已经被删掉的临时文件。
		if h.committing && flags&(fuse.O_WRONLY|fuse.O_RDWR|fuse.O_TRUNC) != 0 {
			done := h.commitDone
			f.mu.Unlock()
			if done != nil {
				<-done
			}
			return f.Open(p, flags)
		}
		hid := f.newHandle(h)
		f.mu.Unlock()
		return 0, hid
	}
	f.mu.Unlock()
	file, ok, err := f.idx.Get(f.ctx, p)
	if err != nil {
		return -fuse.EIO, 0
	}
	f.mu.Lock()
	if ok {
		// 写打开已有文件：必须挂一个待上传节点，否则后续 Truncate/Write 没有临时文件
		if flags&(fuse.O_WRONLY|fuse.O_RDWR|fuse.O_TRUNC) == 0 {
			hid := f.newHandle(&openFile{path: p, size: file.Size})
			f.mu.Unlock()
			return 0, hid
		}
		f.mu.Unlock()

		tmp, err := os.CreateTemp(f.tmpDir, ".mytgfs-upload-*")
		if err != nil {
			debugf("vfs: Open %s temp error: %v", p, err)
			return -fuse.EIO, 0
		}
		h := &openFile{path: p, tmp: tmp, dirty: true, baseMsg: file.MessageID}
		// 没带 O_TRUNC 时保留原内容，避免随机写把前缀丢掉
		if flags&fuse.O_TRUNC == 0 && file.Size > 0 {
			if err := f.copyExisting(file, tmp); err != nil {
				debugf("vfs: Open %s copy existing failed: %v", p, err)
				_ = tmp.Close()
				_ = os.Remove(tmp.Name())
				return -fuse.EIO, 0
			}
			h.size = file.Size
		}
		f.mu.Lock()
		if existing, exists := f.pending[p]; exists {
			f.mu.Unlock()
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
			hid := f.newHandle(existing)
			return 0, hid
		}
		f.pending[p] = h
		hid := f.newHandle(h)
		f.mu.Unlock()
		f.notifyChange(p, fuse.NOTIFY_CREATE)
		return 0, hid
	}
	f.mu.Unlock()

	// 以写方式打开不存在的文件：Windows 走 Open 而不是 Create，这里就地建节点
	if flags&fuse.O_CREAT != 0 || flags&(fuse.O_WRONLY|fuse.O_RDWR) != 0 {
		if rc := f.Mknod(p, 0o666, 0); rc != 0 {
			return rc, 0
		}
		f.mu.Lock()
		h := f.pending[p]
		hid := f.newHandle(h)
		f.mu.Unlock()
		return 0, hid
	}

	// 只读打开不存在的文件：给空句柄，真正读取时按 ENOENT 处理
	f.mu.Lock()
	defer f.mu.Unlock()
	return 0, f.newHandle(nil)
}

// copyExisting 把索引里已有文件的内容复制到临时文件。
//
// 输入：索引记录 file、目标临时文件 tmp
// 输出：error
// 预期：按 4 MiB 分块从 Telegram 拉取，写打开后未截断时保留原内容
func (f *FS) copyExisting(file index.File, tmp *os.File) error {
	const chunk = 4 << 20
	var off int64
	for off < file.Size {
		n := int64(chunk)
		if off+n > file.Size {
			n = file.Size - off
		}
		data, newFile, err := f.readRangeWithRefresh(file, off, int(n), func(done int64) {
			f.trackRead(file.Path, done, file.Size)
		})
		if err != nil {
			return err
		}
		if len(data) == 0 {
			return fmt.Errorf("copyExisting: 读取 %s 在偏移 %d 处返回空数据", file.Path, off)
		}
		if _, err := tmp.WriteAt(data, off); err != nil {
			return err
		}
		file = newFile
		off += int64(len(data))
	}
	return nil
}

// fileSHA256 计算临时文件前 size 字节的 sha256。
//
// 输入：tmp 临时文件；size 有效字节数
// 输出：小写十六进制摘要、error
// 预期：只读前 size 字节（临时文件可能因截断前写入而更长）
func fileSHA256(tmp *os.File, size int64) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, io.NewSectionReader(tmp, 0, size)); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// partOf 把索引记录转成远程读取用的 Part。
func partOf(file index.File) *telegramstore.Part {
	return &telegramstore.Part{
		MessageID:     file.MessageID,
		DocumentID:    file.DocumentID,
		AccessHash:    file.AccessHash,
		FileReference: file.FileReference,
		Size:          file.Size,
	}
}

// readRangeWithRefresh 读远程区间，file_reference 过期时刷新后重试一次。
//
// 输入：索引记录 file、偏移 ofst、长度 length、progress 实际下载进度回调（可为 nil）
// 输出：数据、可能已更新的索引记录、error
// 预期：刷新成功时把新引用写回索引；失败时返回原始错误，不修改索引
func (f *FS) readRangeWithRefresh(file index.File, ofst int64, length int, progress func(done int64)) ([]byte, index.File, error) {
	data, err := f.store.ReadRange(f.ctx, partOf(file), ofst, length, progress)
	if err == nil || !telegramstore.IsFileReferenceError(err) {
		return data, file, err
	}
	debugf("vfs: file_reference 失效 %s msg=%d，按消息 ID 刷新", shortPath(file.Path), file.MessageID)
	np, rerr := f.store.Refresh(f.ctx, file.MessageID)
	if rerr != nil {
		return nil, file, fmt.Errorf("刷新 file_reference: %w", rerr)
	}
	file.DocumentID = np.DocumentID
	file.AccessHash = np.AccessHash
	file.FileReference = np.FileReference
	file.Size = np.Size
	if perr := f.idx.Replace(f.ctx, file); perr != nil {
		debugf("vfs: 刷新引用后写索引失败 %s: %v", shortPath(file.Path), perr)
	}
	data, err = f.store.ReadRange(f.ctx, partOf(file), ofst, length, progress)
	return data, file, err
}

// Read 按偏移读。
//
// 输入：路径 p、缓冲 buff、偏移 ofst、句柄 fh
// 输出：读到的字节数或负 errno
// 预期：句柄或 pending 节点还有临时文件时直接读临时文件，
//
//	这样「刚写完（提交还没完成）」也能读出完整内容；否则走 Telegram 索引
func (f *FS) Read(p string, buff []byte, ofst int64, fh uint64) int {
	p = clean(p)
	f.mu.Lock()
	h := f.handles[fh]
	if h == nil || h.tmp == nil {
		if ph, ok := f.pending[p]; ok && ph.tmp != nil {
			h = ph
		}
	}
	var (
		tmp  *os.File
		size int64
	)
	if h != nil && h.tmp != nil {
		tmp, size = h.tmp, h.size
	}
	f.mu.Unlock()
	if tmp != nil {
		if ofst >= size {
			return 0
		}
		n := len(buff)
		if ofst+int64(n) > size {
			n = int(size - ofst)
		}
		got, err := tmp.ReadAt(buff[:n], ofst)
		if err != nil && err != io.EOF {
			return -fuse.EIO
		}
		return got
	}
	file, ok, err := f.idx.Get(f.ctx, p)
	if err != nil {
		debugf("vfs: Read 索引查询失败 %s: %v", shortPath(p), err)
		return -fuse.EIO
	}
	if !ok {
		debugf("vfs: Read 索引里没有 %s", shortPath(p))
		return -fuse.ENOENT
	}
	if ofst >= file.Size {
		return 0
	}
	length := len(buff)
	if ofst+int64(length) > file.Size {
		length = int(file.Size - ofst)
	}
	if length <= 0 {
		return 0
	}
	data, hit, cerr := f.cacheRead(file, ofst, length)
	if cerr != nil {
		debugf("vfs: Read 远端失败 %s ofst=%d len=%d msg=%d: %v", shortPath(p), ofst, length, file.MessageID, cerr)
		return -fuse.EIO
	}
	if !hit {
		var err error
		data, _, err = f.readRangeWithRefresh(file, ofst, length, func(done int64) {
			f.trackRead(file.Path, done, file.Size)
		})
		if err != nil {
			debugf("vfs: Read 远端失败 %s ofst=%d len=%d msg=%d: %v", shortPath(p), ofst, length, file.MessageID, err)
			return -fuse.EIO
		}
	}
	// 读到文件末尾时清掉进度条目；下载中的条目由 downloader 的实际进度维护
	if ofst+int64(len(data)) >= file.Size {
		f.clearProgress(p)
	}
	return copy(buff, data)
}

// Write 写入临时文件。
func (f *FS) Write(p string, buff []byte, ofst int64, fh uint64) int {
	debugf("vfs: Write %s ofst=%d len=%d fh=%d", p, ofst, len(buff), fh)
	f.mu.Lock()
	h := f.handles[fh]
	f.mu.Unlock()
	if h == nil || h.tmp == nil {
		return -fuse.EBADF
	}
	if _, err := h.tmp.WriteAt(buff, ofst); err != nil {
		return -fuse.EIO
	}
	if ofst+int64(len(buff)) > h.size {
		h.size = ofst + int64(len(buff))
	}
	h.dirty = true
	return len(buff)
}

// Release 关闭文件。
//
// 输入：路径 p、句柄 fh
// 输出：0 或负 errno
// 预期：最后一个句柄关闭时才提交；0 字节文件不占 Telegram 对象，直接写索引
func (f *FS) Release(p string, fh uint64) int {
	debugf("vfs: Release %s fh=%d", p, fh)
	f.dropCache(clean(p))
	f.clearProgress(clean(p))
	f.mu.Lock()
	h := f.handles[fh]
	delete(f.handles, fh)
	if h == nil {
		f.mu.Unlock()
		return 0
	}
	h.refs--
	if h.refs > 0 || h.tmp == nil {
		f.mu.Unlock()
		return 0
	}
	// 已经提交过（或有别的 goroutine 正在提交）时不重复上传
	if h.committing || h.done {
		f.mu.Unlock()
		return 0
	}
	// 提交期间保留 pending 里的节点：名字和内容对读操作仍然可见，
	// 避免「刚写完立刻读」拿到 ENOENT（Windows 上传耗时可达数秒）
	h.committing = true
	h.commitDone = make(chan struct{})
	f.mu.Unlock()

	rc := f.commit(h)
	// 输入：提交结果；输出：无
	// 预期：成功才摘掉 pending 并标记 done；失败保留节点，等下次写打开重试
	f.finishCommit(h, rc == 0)
	return rc
}

// commit 把临时文件内容落到 Telegram 并写索引。
//
// 输入：已标记 committing 的节点 h
// 输出：0 或负 errno
// 预期：与 Rename/Unlink/Mkdir/Rmdir 串行；只有成功才关闭并删除临时文件，
// 失败时保留临时文件，用户仍能读取，也能再写打开重试上传
func (f *FS) commit(h *openFile) int {
	f.opMu.Lock()
	defer f.opMu.Unlock()

	rc := f.commitLocked(h)
	if rc == 0 {
		_ = h.tmp.Close()
		_ = os.Remove(h.tmp.Name())
	}
	return rc
}

func (f *FS) commitLocked(h *openFile) int {
	// 覆盖写：记下旧的索引记录，提交成功后删掉旧消息，避免频道里留孤儿
	old, hadOld, _ := f.idx.Get(f.ctx, h.path)

	// 并发写保护：打开时记下的消息号与当前索引不一致，说明别的端或另一个
	// 写入已经提交了新内容；此刻覆盖会静默丢掉对方的写入，所以拒绝提交。
	// 临时文件保留在 pending 里，用户还能把内容读出来另存。
	if hadOld && old.MessageID > 0 && old.MessageID != h.baseMsg {
		debugf("vfs: %s 已被其他写入更新 msg %d -> %d，拒绝覆盖", shortPath(h.path), h.baseMsg, old.MessageID)
		return -fuse.EBUSY
	}

	if h.size == 0 {
		// 空文件：先删旧记录再写入 0 字节记录，保证「截断为空」也生效
		if err := f.idx.Delete(f.ctx, h.path); err != nil {
			debugf("vfs: Release empty delete %s failed: %v", h.path, err)
			return -fuse.EIO
		}
		if err := f.idx.Put(f.ctx, index.File{
			Path:  h.path,
			Size:  0,
			Mtime: nowUnix(),
		}); err != nil {
			debugf("vfs: Release empty index %s failed: %v", h.path, err)
			return -fuse.EIO
		}
		if hadOld && old.MessageID > 0 {
			if err := f.store.Delete(f.ctx, old.MessageID); err != nil {
				debugf("vfs: Release 空文件删除旧消息 %d 失败: %v", old.MessageID, err)
			}
		}
		f.notifyChange(h.path, fuse.NOTIFY_CREATE)
		return 0
	}
	if _, err := h.tmp.Seek(0, io.SeekStart); err != nil {
		return -fuse.EIO
	}
	// 内容摘要：同目录去重靠它，写进 caption 后冷启动重建索引也能拿到
	sha, err := fileSHA256(h.tmp, h.size)
	if err != nil {
		debugf("vfs: Release 计算摘要 %s 失败: %v", shortPath(h.path), err)
		return -fuse.EIO
	}
	caption := tgsync.EncodeCaptionHash(h.path, sha)

	// 同路径内容没变：不用新建消息，引用数也不变，只把摘要和时间写回索引
	if hadOld && old.Sha256 != "" && old.Sha256 == sha && old.Size == h.size {
		old.Mtime = nowUnix()
		if err := f.idx.Replace(f.ctx, old); err != nil {
			debugf("vfs: 内容未变写索引失败 %s: %v", shortPath(h.path), err)
			return -fuse.EIO
		}
		debugf("vfs: %s 内容未变，跳过上传 msg=%d", shortPath(h.path), old.MessageID)
		f.notifyChange(h.path, fuse.NOTIFY_CREATE)
		return 0
	}

	// 从这里到上传成功之间可能崩溃或断网：先落一条待补传记录，
	// 重启后 ResumePending 会把临时文件继续传完，内容不会丢
	if err := f.idx.PutPending(f.ctx, index.PendingUpload{
		Path:  h.path,
		Temp:  h.tmp.Name(),
		Size:  h.size,
		Mtime: nowUnix(),
	}); err != nil {
		debugf("vfs: 记录待补传 %s 失败: %v", shortPath(h.path), err)
		return -fuse.EIO
	}

	// 频道里已有相同内容：不传输字节，只加一条引用同一 document 的消息。
	// 文件本体只有一份，多个路径各持一条引用；删除路径只摘自己的引用。
	dup, found, derr := f.idx.FindContent(f.ctx, path.Dir(h.path), h.size, sha)
	if derr != nil {
		debugf("vfs: 查询同内容文件失败 %s: %v", shortPath(h.path), derr)
	}
	var reuse *index.File
	if found {
		reuse = &dup
	}
	part, err := f.uploadPart(h, caption, reuse)
	if err != nil {
		debugf("vfs: Release 上传 %s 失败: %v", shortPath(h.path), err)
		return -fuse.EIO
	}
	if reuse != nil {
		debugf("vfs: %s 复用 %s 的 document=%d msg=%d",
			shortPath(h.path), shortPath(reuse.Path), reuse.DocumentID, part.MessageID)
	}
	if err := f.idx.Put(f.ctx, index.File{
		Path:          h.path,
		MessageID:     part.MessageID,
		DocumentID:    part.DocumentID,
		AccessHash:    part.AccessHash,
		FileReference: part.FileReference,
		Size:          part.Size,
		Mtime:         nowUnix(),
		Sha256:        sha,
	}); err != nil {
		debugf("vfs: Release index %s failed: %v", h.path, err)
		return -fuse.EIO
	}
	if err := f.idx.DeletePending(f.ctx, h.path); err != nil {
		debugf("vfs: 清除待补传记录 %s 失败: %v", shortPath(h.path), err)
	}
	if hadOld && old.MessageID > 0 {
		if err := f.store.Delete(f.ctx, old.MessageID); err != nil {
			debugf("vfs: Release 删除旧消息 %d 失败: %v", old.MessageID, err)
		}
	}
	f.notifyChange(h.path, fuse.NOTIFY_CREATE)
	return 0
}

// finishCommit 收尾提交状态。
//
// 输入：节点 h、是否成功
// 输出：无
// 预期：成功时摘掉 pending（仅当它仍指向 h）并标记 done；
//
//	失败时清掉 committing 让节点留在 pending 里，下次写打开可重试上传
func (f *FS) finishCommit(h *openFile, ok bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !ok {
		h.committing = false
	}
	if f.pending[h.path] == h {
		if ok {
			delete(f.pending, h.path)
		}
		h.done = ok
	}
	if h.commitDone != nil {
		close(h.commitDone)
		h.commitDone = nil
	}
}

// uploadAttempts 是一次提交里最多尝试的上传次数（含首次）。
const uploadAttempts = 3

// uploadPart 上传或引用文件内容，失败时按 retryBase 退避重试。
//
// 输入：h 提交中的节点；caption 说明；reuse 非空表示复用已有 document
// 输出：Part；error
// 预期：每次尝试前把临时文件定位到开头；ErrTooLarge 不重试（重试也不会成功）
func (f *FS) uploadPart(h *openFile, caption string, reuse *index.File) (*telegramstore.Part, error) {
	delay := f.retryBase
	var lastErr error
	for attempt := 1; attempt <= uploadAttempts; attempt++ {
		if _, err := h.tmp.Seek(0, io.SeekStart); err != nil {
			return nil, err
		}
		var part *telegramstore.Part
		var err error
		if reuse != nil {
			part, err = f.store.Copy(f.ctx, partOf(*reuse), path.Base(h.path), "application/octet-stream", caption)
		} else {
			part, err = f.store.Upload(f.ctx, io.LimitReader(h.tmp, h.size), h.size,
				path.Base(h.path), "application/octet-stream", caption)
		}
		if err == nil {
			return part, nil
		}
		lastErr = err
		if errors.Is(err, telegramstore.ErrTooLarge) || attempt == uploadAttempts {
			break
		}
		debugf("vfs: 上传 %s 第 %d 次失败: %v，%s 后重试", shortPath(h.path), attempt, err, delay)
		if delay > 0 {
			time.Sleep(delay)
		}
		delay *= 2
	}
	return nil, lastErr
}

// ResumePending 补传上次崩溃或断网遗留的写入。
//
// 输入：无
// 输出：无
// 预期：挂载启动后在后台调用；成功的记录连同临时文件一起清掉，
// 失败的保留下来等下次启动再试
func (f *FS) ResumePending() {
	rows, err := f.idx.ListPending(f.ctx)
	if err != nil {
		slog.Warn("vfs: 读取待补传记录失败", "err", err)
		return
	}
	for _, p := range rows {
		if f.ctx.Err() != nil {
			return
		}
		f.resumeOne(p)
	}
}

// resumeOne 补传一条记录。
//
// 输入：p 待补传记录
// 输出：无
// 预期：临时文件缺失或大小不符时清掉记录；
// 补传成功后索引指向新消息，临时文件由 commit 删除
func (f *FS) resumeOne(p index.PendingUpload) {
	info, err := os.Stat(p.Temp)
	if err != nil || info.Size() != p.Size {
		// 临时文件已不在或大小不符：没法补传，清掉记录免得每次启动重试
		_ = f.idx.DeletePending(f.ctx, p.Path)
		slog.Warn("vfs: 待补传的临时文件不可用", "path", shortPath(p.Path), "temp", p.Temp)
		return
	}
	tmp, err := os.Open(p.Temp)
	if err != nil {
		slog.Warn("vfs: 打开待补传临时文件失败", "path", shortPath(p.Path), "err", err)
		return
	}
	base := 0
	if cur, ok, gerr := f.idx.Get(f.ctx, p.Path); gerr == nil && ok {
		base = cur.MessageID
	}
	h := &openFile{path: p.Path, tmp: tmp, size: p.Size, baseMsg: base, dirty: true, refs: 1}
	if rc := f.commit(h); rc != 0 {
		_ = tmp.Close()
		slog.Warn("vfs: 补传失败，下次启动重试", "path", shortPath(p.Path), "rc", rc)
		return
	}
	slog.Info("vfs: 已补传上次未完成的写入", "path", shortPath(p.Path), "size", p.Size)
}

// Unlink 删除文件：先删消息，再删索引。
func (f *FS) Unlink(p string) int {
	f.opMu.Lock()
	defer f.opMu.Unlock()
	p = clean(p)
	f.dropCache(p)
	f.clearProgress(p)
	// 该路径若有崩溃遗留的补传记录，一并清掉，避免下次启动又把它传上去
	if err := f.idx.DeletePending(f.ctx, p); err != nil {
		debugf("vfs: Unlink 清除待补传记录 %s 失败: %v", shortPath(p), err)
	}

	// 未提交的节点直接丢弃，避免临时文件泄漏
	f.mu.Lock()
	ph, hadPending := f.pending[p]
	if hadPending {
		delete(f.pending, p)
	}
	f.mu.Unlock()
	if hadPending && ph.tmp != nil {
		_ = ph.tmp.Close()
		_ = os.Remove(ph.tmp.Name())
	}

	file, ok, err := f.idx.Get(f.ctx, p)
	if err != nil {
		return -fuse.EIO
	}
	if !ok {
		if hadPending {
			f.notifyChange(p, fuse.NOTIFY_UNLINK)
			return 0
		}
		return -fuse.ENOENT
	}
	if err := f.store.Delete(f.ctx, file.MessageID); err != nil {
		return -fuse.EIO
	}
	if err := f.idx.Delete(f.ctx, p); err != nil {
		return -fuse.EIO
	}
	f.notifyChange(p, fuse.NOTIFY_UNLINK)
	return 0
}

// Mkdir 创建目录；空目录写入索引，刷新后仍然存在。
func (f *FS) Mkdir(p string, mode uint32) int {
	f.opMu.Lock()
	defer f.opMu.Unlock()
	p = clean(p)
	if f.isDir(p) {
		return -fuse.EEXIST
	}
	if _, ok, err := f.idx.Get(f.ctx, p); err != nil {
		return -fuse.EIO
	} else if ok {
		return -fuse.EEXIST
	}
	if err := f.idx.PutDir(f.ctx, p); err != nil {
		return -fuse.EIO
	}
	f.notifyChange(p, fuse.NOTIFY_MKDIR)
	return 0
}

// Rmdir 删除空目录；非空目录返回 ENOTEMPTY。
func (f *FS) Rmdir(p string) int {
	f.opMu.Lock()
	defer f.opMu.Unlock()
	p = clean(p)
	if !f.isDir(p) {
		return -fuse.ENOENT
	}
	entries, err := f.idx.List(f.ctx, p)
	if err != nil {
		return -fuse.EIO
	}
	if len(entries) > 0 {
		return -fuse.ENOTEMPTY
	}
	if err := f.idx.DeleteDir(f.ctx, p); err != nil {
		return -fuse.EIO
	}
	f.notifyChange(p, fuse.NOTIFY_RMDIR)
	return 0
}

// Rename 改名或移动：改 caption + 更新索引；目录按前缀整体移动。
func (f *FS) Rename(oldpath, newpath string) int {
	f.opMu.Lock()
	defer f.opMu.Unlock()
	oldp := clean(oldpath)
	newp := clean(newpath)
	f.dropCache(oldp)
	f.dropCache(newp)
	if oldp == newp {
		return 0
	}

	file, isFile, err := f.idx.Get(f.ctx, oldp)
	if err != nil {
		debugf("vfs: Rename 读索引 %s 失败: %v", oldp, err)
		return -fuse.EIO
	}
	if isFile {
		target, targetOK, terr := f.idx.Get(f.ctx, newp)
		if terr != nil {
			debugf("vfs: Rename 读目标索引 %s 失败: %v", newp, terr)
			return -fuse.EIO
		}
		if err := f.store.Rename(f.ctx, file.MessageID, tgsync.EncodeCaption(newp)); err != nil {
			debugf("vfs: Rename 改 caption %s->%s 失败: %v", oldp, newp, err)
			return -fuse.EIO
		}
		if err := f.idx.Delete(f.ctx, oldp); err != nil {
			debugf("vfs: Rename 删旧索引 %s 失败: %v", oldp, err)
			return -fuse.EIO
		}
		file.Path = newp
		if targetOK {
			// 覆盖目标：先删目标旧行，避免 Put 的「消息 ID 取最大」留下目标旧记录
			if err := f.idx.Delete(f.ctx, newp); err != nil {
				debugf("vfs: Rename 删目标索引 %s 失败: %v", newp, err)
				return -fuse.EIO
			}
		}
		if err := f.idx.Put(f.ctx, file); err != nil {
			debugf("vfs: Rename 写新索引 %s 失败: %v", newp, err)
			return -fuse.EIO
		}
		// 覆盖已有目标时删掉目标旧消息，避免频道里留孤儿
		if targetOK && target.MessageID != file.MessageID {
			if err := f.store.Delete(f.ctx, target.MessageID); err != nil {
				debugf("vfs: Rename 删除目标旧消息 %d 失败: %v", target.MessageID, err)
			}
		}
		f.notifyChange(oldp, fuse.NOTIFY_UNLINK)
		f.notifyChange(newp, fuse.NOTIFY_CREATE)
		return 0
	}

	if !f.isDir(oldp) {
		return -fuse.ENOENT
	}
	if f.isDir(newp) {
		return -fuse.EEXIST
	}

	dirs, err := f.idx.Dirs(f.ctx, oldp)
	if err != nil {
		debugf("vfs: Rename 读目录 %s 失败: %v", oldp, err)
		return -fuse.EIO
	}
	for _, d := range dirs {
		np := newp + strings.TrimPrefix(d, oldp)
		if err := f.idx.DeleteDir(f.ctx, d); err != nil {
			debugf("vfs: Rename 删目录 %s 失败: %v", d, err)
			return -fuse.EIO
		}
		if err := f.idx.PutDir(f.ctx, np); err != nil {
			debugf("vfs: Rename 写目录 %s 失败: %v", np, err)
			return -fuse.EIO
		}
	}
	files, err := f.idx.ListPrefix(f.ctx, oldp)
	if err != nil {
		debugf("vfs: Rename 读子文件 %s 失败: %v", oldp, err)
		return -fuse.EIO
	}
	for _, ff := range files {
		np := newp + strings.TrimPrefix(ff.Path, oldp)
		if err := f.store.Rename(f.ctx, ff.MessageID, tgsync.EncodeCaption(np)); err != nil {
			debugf("vfs: Rename 改 caption %s->%s 失败: %v", ff.Path, np, err)
			return -fuse.EIO
		}
		if err := f.idx.Delete(f.ctx, ff.Path); err != nil {
			debugf("vfs: Rename 删旧索引 %s 失败: %v", ff.Path, err)
			return -fuse.EIO
		}
		ff.Path = np
		if err := f.idx.Put(f.ctx, ff); err != nil {
			debugf("vfs: Rename 写新索引 %s 失败: %v", np, err)
			return -fuse.EIO
		}
	}
	f.notifyChange(oldp, fuse.NOTIFY_UNLINK)
	f.notifyChange(newp, fuse.NOTIFY_CREATE)
	return 0
}

// Truncate 调整待上传文件大小。
func (f *FS) Truncate(p string, size int64, fh uint64) int {
	debugf("vfs: Truncate %s size=%d fh=%d", p, size, fh)
	f.dropCache(clean(p))
	f.mu.Lock()
	h := f.handles[fh]
	if h == nil {
		h = f.pending[clean(p)]
	}
	f.mu.Unlock()
	if h == nil || h.tmp == nil {
		return -fuse.ENOENT
	}
	if err := h.tmp.Truncate(size); err != nil {
		return -fuse.EIO
	}
	h.size = size
	h.dirty = true
	return 0
}

// Statfs 返回虚拟容量。
//
// 输入：路径、输出结构
// 输出：0
// 预期：总容量 = 2^63-1 字节；已用 = 索引里文件大小合计，按 4096 字节簇向上取整
func (f *FS) Statfs(p string, stat *fuse.Statfs_t) int {
	const blockSize = 4096
	total := int64(^uint64(0) >> 1) // 2^63-1

	used, err := f.idx.Used(f.ctx)
	if err != nil || used < 0 {
		debugf("vfs: Statfs Used 失败: %v (used=%d)", err, used)
		used = 0
	}
	if used > total {
		used = total
	}
	// Windows 按 4096 字节簇向下取整显示，used 不足一个簇会被算成 0，
	// 这里按簇向上取整，保证有文件时已用至少一个簇
	usedBlocks := uint64(used+blockSize-1) / blockSize
	blocks := uint64(total / blockSize)
	freeBlocks := blocks
	if usedBlocks < blocks {
		freeBlocks = blocks - usedBlocks
	} else {
		freeBlocks = 0
	}
	debugf("vfs: Statfs used=%d usedBlocks=%d freeBlocks=%d", used, usedBlocks, freeBlocks)

	stat.Bsize = blockSize
	stat.Frsize = blockSize
	stat.Blocks = blocks
	stat.Bfree = freeBlocks
	stat.Bavail = freeBlocks
	stat.Files = 1 << 20
	stat.Ffree = stat.Files / 2
	stat.Namemax = 255
	return 0
}

// isDir 判断路径是否是目录（显式目录或由子项推导）。
func (f *FS) isDir(p string) bool {
	ok, err := f.idx.IsDir(f.ctx, p)
	return err == nil && ok
}

// clean 规整路径。
func clean(p string) string {
	p = path.Clean("/" + strings.TrimPrefix(p, "/"))
	if p == "." {
		return "/"
	}
	return p
}

// shortPath 压缩超长路径，避免日志被 1000+ 字符的深路径刷屏。
//
// 输入：任意路径
// 输出：长度 <= 80 时原样返回；否则「前 40 + ... + 后 28」并附总长
// 预期：日志里仍能区分不同深路径，又不至于爆行
func shortPath(p string) string {
	const keep = 40
	if len(p) <= keep+40 {
		return p
	}
	return fmt.Sprintf("%s...%s(len=%d)", p[:keep], p[len(p)-28:], len(p))
}
