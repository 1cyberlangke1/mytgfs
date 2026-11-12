package vfs

// FUSE 文件系统适配层。
//
// 元数据读本地索引，文件内容走 telegramstore 随机读。
// v1 支持：读文件、写文件、删除文件、目录由 path 推导。
// 不支持：硬链接、软链接、重命名（返回 ENOSYS）。

import (
	"context"
	"io"
	"log"
	"os"
	"path"
	"strings"
	"sync"

	"github.com/winfsp/cgofuse/fuse"

	"mytgfs/internal/index"
	"mytgfs/internal/telegramstore"
	tgsync "mytgfs/internal/sync"
)

// debug 由 MYTGFS_DEBUG=1 打开，默认关闭。
var debug = os.Getenv("MYTGFS_DEBUG") != ""

func debugf(format string, args ...any) {
	if debug {
		log.Printf(format, args...)
	}
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
	next    uint64
}

type openFile struct {
	path  string
	tmp   *os.File
	dirty bool
	size  int64
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
	}
}

// Getattr 取文件属性。
func (f *FS) Getattr(p string, stat *fuse.Stat_t, fh uint64) int {
	p = clean(p)
	if p == "/" {
		stat.Mode = fuse.S_IFDIR | 0o777
		stat.Nlink = 2
		return 0
	}
	// 正在写入、尚未上传的文件
	f.mu.Lock()
	for _, h := range f.handles {
		if h.path == p && h.tmp != nil {
			size := h.size
			f.mu.Unlock()
			stat.Mode = fuse.S_IFREG | 0o666
			stat.Size = size
			stat.Nlink = 1
			return 0
		}
	}
	f.mu.Unlock()
	file, ok, err := f.idx.Get(f.ctx, p)
	if err != nil {
		return -fuse.EIO
	}
	if ok {
		stat.Mode = fuse.S_IFREG | 0o666
		stat.Size = file.Size
		stat.Nlink = 1
		return 0
	}
	if f.isDir(p) {
		stat.Mode = fuse.S_IFDIR | 0o777
		stat.Nlink = 2
		return 0
	}
	return -fuse.ENOENT
}

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
		st := &fuse.Stat_t{Mode: fuse.S_IFDIR | 0o777, Nlink: 2}
		if !e.IsDir {
			st.Mode = fuse.S_IFREG | 0o666
			st.Nlink = 1
			if e.File != nil {
				st.Size = e.File.Size
			}
		}
		if !fill(e.Name, st, 0) {
			return 0
		}
	}
	return 0
}

// Open 打开文件。
func (f *FS) Open(p string, flags int) (int, uint64) {
	p = clean(p)
	file, ok, err := f.idx.Get(f.ctx, p)
	if err != nil {
		return -fuse.EIO, 0
	}
	if !ok {
		return -fuse.ENOENT, 0
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.next++
	hid := f.next
	f.handles[hid] = &openFile{path: p, size: file.Size}
	return 0, hid
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

// Create 新建文件（写入临时文件，Release 时上传）。
func (f *FS) Create(p string, flags int, mode uint32) (int, uint64) {
	debugf("vfs: Create %s flags=%d", p, flags)
	p = clean(p)
	tmp, err := os.CreateTemp(f.tmpDir, ".mytgfs-upload-*")
	if err != nil {
		return -fuse.EIO, 0
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.next++
	hid := f.next
	f.handles[hid] = &openFile{path: p, tmp: tmp, dirty: true}
	return 0, hid
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

// Release 关闭文件；有内容时上传并更新索引。
func (f *FS) Release(p string, fh uint64) int {
	debugf("vfs: Release %s fh=%d", p, fh)
	f.mu.Lock()
	h := f.handles[fh]
	delete(f.handles, fh)
	f.mu.Unlock()
	if h == nil || h.tmp == nil {
		return 0
	}
	defer h.tmp.Close()
	defer os.Remove(h.tmp.Name())
	if !h.dirty || h.size == 0 {
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

// Truncate 暂不支持。
func (f *FS) Truncate(p string, size int64, fh uint64) int { return -fuse.ENOSYS }

// Statfs 返回虚拟容量。
func (f *FS) Statfs(p string, stat *fuse.Statfs_t) int {
	const blockSize = 4096
	total := int64(^uint64(0) >> 1) // 2^63-1
	stat.Bsize = blockSize
	stat.Frsize = blockSize
	stat.Blocks = uint64(total / blockSize)
	stat.Bfree = stat.Blocks / 2
	stat.Bavail = stat.Bfree
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
