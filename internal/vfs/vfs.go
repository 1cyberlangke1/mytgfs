package vfs

// Package vfs 是 FUSE 适配层。
//
// 数据流：元数据读本地 SQLite 索引，文件内容按需走 telegramstore 随机读；
// 写入先落磁盘临时文件（不占内存），Release 时上传 Telegram 并写入索引。
// 支持：读、写、截断、删除、重命名；目录由 path 推导，不单独存储。
// 不支持：硬链接、软链接。

import (
	"context"
	"fmt"
	"hash/fnv"
	"io"
	"log/slog"
	"os"
	"path"
	"strings"
	"sync"

	"github.com/winfsp/cgofuse/fuse"

	"mytgfs/internal/index"
	"mytgfs/internal/telegramstore"
	tgsync "mytgfs/internal/sync"
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
	store  *telegramstore.Store
	ctx    context.Context
	tmpDir string

	mu      sync.Mutex
	handles map[uint64]*openFile
	pending map[string]*openFile
	next    uint64
}

type openFile struct {
	path  string
	tmp   *os.File
	dirty bool
	size  int64
	// refs 是同时打开该节点的 handle 数；归零才允许提交并清理 pending。
	refs int
}

// New 创建文件系统。
//
// 输入：ctx；idx 索引；store 存储；tmpDir 写入临时目录
// 输出：*FS
// 预期：tmpDir 为空时使用系统临时目录
func New(ctx context.Context, idx *index.Index, store *telegramstore.Store, tmpDir string) *FS {
	if tmpDir == "" {
		tmpDir = os.TempDir()
	}
	return &FS{
		idx:     idx,
		store:   store,
		ctx:     ctx,
		tmpDir:  tmpDir,
		handles: map[uint64]*openFile{},
		pending: map[string]*openFile{},
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
	return 0, hid
}

// Open 打开文件。
//
// 输入：路径 p、flags
// 输出：0 与句柄（永不返回 ENOENT）
// 预期：命中待上传节点用同一个节点加引用；命中索引给只读句柄；
//       都未命中时给空句柄，让上层自己拿到 EBADF，而不是在这里抛 ENOENT
func (f *FS) Open(p string, flags int) (int, uint64) {
	debugf("vfs: Open %s flags=%d", p, flags)
	p = clean(p)
	f.mu.Lock()
	if h, ok := f.pending[p]; ok {
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
		h := &openFile{path: p, tmp: tmp, dirty: true}
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
	part := &telegramstore.Part{
		MessageID:     file.MessageID,
		DocumentID:    file.DocumentID,
		AccessHash:    file.AccessHash,
		FileReference: file.FileReference,
		Size:          file.Size,
	}
	const chunk = 4 << 20
	var off int64
	for off < file.Size {
		n := int64(chunk)
		if off+n > file.Size {
			n = file.Size - off
		}
		data, err := f.store.ReadRange(f.ctx, part, off, int(n))
		if err != nil {
			return err
		}
		if len(data) == 0 {
			return fmt.Errorf("copyExisting: 读取 %s 在偏移 %d 处返回空数据", file.Path, off)
		}
		if _, err := tmp.WriteAt(data, off); err != nil {
			return err
		}
		off += int64(len(data))
	}
	return nil
}

// Read 按偏移读。
func (f *FS) Read(p string, buff []byte, ofst int64, fh uint64) int {
	p = clean(p)
	file, ok, err := f.idx.Get(f.ctx, p)
	if err != nil {
		return -fuse.EIO
	}
	if !ok {
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
	part := &telegramstore.Part{
		MessageID:     file.MessageID,
		DocumentID:    file.DocumentID,
		AccessHash:    file.AccessHash,
		FileReference: file.FileReference,
		Size:          file.Size,
	}
	data, err := f.store.ReadRange(f.ctx, part, ofst, length)
	if err != nil {
		return -fuse.EIO
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
	// 提交前先摘掉 pending，避免并发 Open 再挂到同一节点上重复提交
	delete(f.pending, h.path)
	f.mu.Unlock()

	defer h.tmp.Close()
	defer os.Remove(h.tmp.Name())

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
		f.mu.Lock()
		delete(f.pending, h.path)
		f.mu.Unlock()
		return 0
	}
	if _, err := h.tmp.Seek(0, io.SeekStart); err != nil {
		return -fuse.EIO
	}
	caption := tgsync.EncodeCaption(h.path)
	part, err := f.store.Upload(f.ctx, io.LimitReader(h.tmp, h.size), h.size,
		path.Base(h.path), "application/octet-stream", caption)
	if err != nil {
		debugf("vfs: Release upload %s failed: %v", h.path, err)
		return -fuse.EIO
	}
	if err := f.idx.Put(f.ctx, index.File{
		Path:          h.path,
		MessageID:     part.MessageID,
		DocumentID:    part.DocumentID,
		AccessHash:    part.AccessHash,
		FileReference: part.FileReference,
		Size:          part.Size,
		Mtime:         nowUnix(),
	}); err != nil {
		debugf("vfs: Release index %s failed: %v", h.path, err)
		return -fuse.EIO
	}
	f.mu.Lock()
	delete(f.pending, h.path)
	f.mu.Unlock()
	return 0
}

// Unlink 删除文件：先删消息，再删索引。
func (f *FS) Unlink(p string) int {
	p = clean(p)
	file, ok, err := f.idx.Get(f.ctx, p)
	if err != nil {
		return -fuse.EIO
	}
	if !ok {
		return -fuse.ENOENT
	}
	if err := f.store.Delete(f.ctx, file.MessageID); err != nil {
		return -fuse.EIO
	}
	if err := f.idx.Delete(f.ctx, p); err != nil {
		return -fuse.EIO
	}
	return 0
}

// Mkdir 目录由 path 推导，这里直接成功。
func (f *FS) Mkdir(p string, mode uint32) int { return 0 }

// Rmdir 目录由 path 推导，这里直接成功。
func (f *FS) Rmdir(p string) int { return 0 }

// Rename 改名或移动：改 caption + 更新索引。
func (f *FS) Rename(oldpath, newpath string) int {
	oldp := clean(oldpath)
	newp := clean(newpath)
	file, ok, err := f.idx.Get(f.ctx, oldp)
	if err != nil {
		return -fuse.EIO
	}
	if !ok {
		return -fuse.ENOENT
	}
	if err := f.store.Rename(f.ctx, file.MessageID, tgsync.EncodeCaption(newp)); err != nil {
		return -fuse.EIO
	}
	if err := f.idx.Delete(f.ctx, oldp); err != nil {
		return -fuse.EIO
	}
	file.Path = newp
	if err := f.idx.Put(f.ctx, file); err != nil {
		return -fuse.EIO
	}
	return 0
}

// Truncate 调整待上传文件大小。
func (f *FS) Truncate(p string, size int64, fh uint64) int {
	debugf("vfs: Truncate %s size=%d fh=%d", p, size, fh)
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

// isDir 判断路径是否是目录（由子项推导）。
func (f *FS) isDir(p string) bool {
	parent := path.Dir(p)
	base := path.Base(p)
	entries, err := f.idx.List(f.ctx, parent)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir && e.Name == base {
			return true
		}
	}
	return false
}

// clean 规整路径。
func clean(p string) string {
	p = path.Clean("/" + strings.TrimPrefix(p, "/"))
	if p == "." {
		return "/"
	}
	return p
}
