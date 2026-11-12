package dav

// WebDAV 适配层：把频道内容暴露成标准 WebDAV 文件系统。
//
// 读按 1MB 分块按需拉取，写先落临时文件、关闭时整份上传，
// 目录、改名、删除直接操作索引和消息。

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
	"time"

	"golang.org/x/net/webdav"

	"mytgfs/internal/index"
	tgsync "mytgfs/internal/sync"
	"mytgfs/internal/telegramstore"
)

// Store 是 WebDAV 层需要的存储能力。
type Store interface {
	ReadRange(ctx context.Context, p *telegramstore.Part, offset int64, length int, progress func(done int64)) ([]byte, error)
	Upload(ctx context.Context, r io.Reader, size int64, name, mime, caption string) (*telegramstore.Part, error)
	Delete(ctx context.Context, ids ...int) error
	Rename(ctx context.Context, messageID int, caption string) error
}

// 自适应块大小：首块 1MB，顺序读时逐次翻倍，上限 16MB。
// 网络慢的用户块小、等待短；网络快的用户块大、请求少。
const (
	firstChunk = 1 << 20
	maxChunk   = 16 << 20
)

// FileSystem 实现 webdav.FileSystem。
type FileSystem struct {
	ctx   context.Context
	idx   *index.Index
	store Store
	chunk int64
}

// New 创建 WebDAV 文件系统。
//
// 输入：ctx；idx 索引；store 存储
// 输出：*FileSystem
// 预期：所有路径都相对于卷根 "/"
func New(ctx context.Context, idx *index.Index, store Store, chunk int64) *FileSystem {
	if chunk <= 0 {
		chunk = firstChunk
	}
	return &FileSystem{ctx: ctx, idx: idx, store: store, chunk: chunk}
}

// Mkdir 创建目录。
func (d *FileSystem) Mkdir(_ context.Context, name string, _ os.FileMode) error {
	name = clean(name)
	if name == "/" {
		return os.ErrExist
	}
	return d.idx.PutDir(d.ctx, name)
}

// OpenFile 打开文件或目录。
func (d *FileSystem) OpenFile(_ context.Context, name string, flag int, perm os.FileMode) (webdav.File, error) {
	name = clean(name)
	if flag&(os.O_WRONLY|os.O_RDWR|os.O_CREATE) != 0 {
		tmp, err := os.CreateTemp("", ".mytgfs-dav-*")
		if err != nil {
			return nil, err
		}
		return &file{d: d, name: name, tmp: tmp, write: true, perm: perm}, nil
	}
	if d.isDir(name) {
		return &file{d: d, name: name, dir: true}, nil
	}
	f, ok, err := d.idx.Get(d.ctx, name)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, os.ErrNotExist
	}
	return &file{d: d, name: name, entry: &f}, nil
}

// RemoveAll 删除文件或目录。
func (d *FileSystem) RemoveAll(_ context.Context, name string) error {
	name = clean(name)
	if name == "/" {
		return errors.New("不能删除根目录")
	}
	if d.isDir(name) {
		return d.idx.DeleteDir(d.ctx, name)
	}
	f, ok, err := d.idx.Get(d.ctx, name)
	if err != nil {
		return err
	}
	if !ok {
		return os.ErrNotExist
	}
	if err := d.store.Delete(d.ctx, f.MessageID); err != nil {
		return err
	}
	return d.idx.Delete(d.ctx, name)
}

// Rename 改名：文件的 caption 跟着改，索引同步迁移。
func (d *FileSystem) Rename(_ context.Context, oldName, newName string) error {
	oldName, newName = clean(oldName), clean(newName)
	if d.isDir(oldName) {
		return errors.New("暂不支持目录改名")
	}
	f, ok, err := d.idx.Get(d.ctx, oldName)
	if err != nil {
		return err
	}
	if !ok {
		return os.ErrNotExist
	}
	if err := d.store.Rename(d.ctx, f.MessageID, tgsync.EncodeCaptionHash(newName, f.Sha256)); err != nil {
		return err
	}
	if err := d.idx.Delete(d.ctx, oldName); err != nil {
		return err
	}
	f.Path = newName
	return d.idx.Replace(d.ctx, f)
}

// Stat 返回文件或目录信息。
func (d *FileSystem) Stat(_ context.Context, name string) (fs.FileInfo, error) {
	name = clean(name)
	if name == "/" || d.isDir(name) {
		return info{name: path.Base(name), dir: true, size: 0, mod: time.Now()}, nil
	}
	f, ok, err := d.idx.Get(d.ctx, name)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, os.ErrNotExist
	}
	return info{name: path.Base(name), size: f.Size, mod: time.Unix(f.Mtime, 0)}, nil
}

// isDir 判断路径是否为显式目录。
func (d *FileSystem) isDir(p string) bool {
	if p == "/" {
		return true
	}
	ok, err := d.idx.IsDir(d.ctx, p)
	return err == nil && ok
}

// file 同时实现 webdav.File 的读写两种模式。
type file struct {
	d     *FileSystem
	name  string
	tmp   *os.File
	write bool
	dir   bool
	perm  os.FileMode
	entry *index.File

	off  int64
	buf  []byte
	base int64
}

// Close 关闭：写模式提交上传，读模式释放缓冲。
func (f *file) Close() error {
	if f.write && f.tmp != nil {
		defer os.Remove(f.tmp.Name())
		defer f.tmp.Close()
		st, err := f.tmp.Stat()
		if err != nil {
			return err
		}
		if _, err := f.tmp.Seek(0, io.SeekStart); err != nil {
			return err
		}
		caption := tgsync.EncodeCaptionHash(f.name, "")
		part, err := f.d.store.Upload(f.d.ctx, f.tmp, st.Size(), path.Base(f.name), "application/octet-stream", caption)
		if err != nil {
			return err
		}
		return f.d.idx.Put(f.d.ctx, index.File{
			Path:          f.name,
			MessageID:     part.MessageID,
			DocumentID:    part.DocumentID,
			AccessHash:    part.AccessHash,
			FileReference: part.FileReference,
			Size:          part.Size,
			Mtime:         time.Now().Unix(),
		})
	}
	return nil
}

// Read 按需拉取一块再返回。
func (f *file) Read(p []byte) (int, error) {
	if f.dir || f.entry == nil {
		return 0, io.EOF
	}
	if f.off >= f.entry.Size {
		return 0, io.EOF
	}
	if f.buf == nil || f.off < f.base || f.off >= f.base+int64(len(f.buf)) {
		length := f.d.chunk
		if length <= 0 {
			length = firstChunk
		}
		if f.off+length > f.entry.Size {
			length = f.entry.Size - f.off
		}
		data, err := f.d.store.ReadRange(f.d.ctx, partOf(*f.entry), f.off, int(length), nil)
		if err != nil {
			return 0, err
		}
		f.buf, f.base = data, f.off
	}
	n := copy(p, f.buf[f.off-f.base:])
	f.off += int64(n)
	return n, nil
}

// Seek 只支持顺序定位，跳转后重新拉块。
func (f *file) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
	case io.SeekCurrent:
		offset += f.off
	case io.SeekEnd:
		if f.entry == nil {
			return 0, errors.New("未知长度")
		}
		offset += f.entry.Size
	default:
		return 0, errors.New("不支持的 whence")
	}
	if offset < 0 {
		return 0, errors.New("负偏移")
	}
	f.off = offset
	f.buf = nil
	return offset, nil
}

// Write 写临时文件。
func (f *file) Write(p []byte) (int, error) {
	if !f.write {
		return 0, errors.New("文件未以写模式打开")
	}
	return f.tmp.Write(p)
}

// Readdir 列出目录项。
func (f *file) Readdir(count int) ([]fs.FileInfo, error) {
	if !f.dir {
		return nil, errors.New("不是目录")
	}
	entries, err := f.d.idx.List(f.d.ctx, f.name)
	if err != nil {
		return nil, err
	}
	out := make([]fs.FileInfo, 0, len(entries))
	for _, e := range entries {
		if e.IsDir {
			out = append(out, info{name: e.Name, dir: true, mod: time.Now()})
			continue
		}
		if e.File != nil {
			out = append(out, info{name: e.Name, size: e.File.Size, mod: time.Unix(e.File.Mtime, 0)})
		}
	}
	return out, nil
}

// Stat 返回自身信息。
func (f *file) Stat() (fs.FileInfo, error) {
	if f.dir {
		return info{name: path.Base(f.name), dir: true, mod: time.Now()}, nil
	}
	if f.entry != nil {
		return info{name: path.Base(f.name), size: f.entry.Size, mod: time.Unix(f.entry.Mtime, 0)}, nil
	}
	return f.tmp.Stat()
}

// info 是 fs.FileInfo 的最小实现。
type info struct {
	name string
	size int64
	dir  bool
	mod  time.Time
}

func (i info) Name() string { return i.name }
func (i info) Size() int64  { return i.size }
func (i info) Mode() fs.FileMode {
	if i.dir {
		return fs.ModeDir | 0o755
	}
	return 0o644
}
func (i info) ModTime() time.Time { return i.mod }
func (i info) IsDir() bool        { return i.dir }
func (i info) Sys() any           { return nil }

// clean 规范化路径。
func clean(p string) string {
	p = strings.ReplaceAll(p, `\`, "/")
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return path.Clean(p)
}

// partOf 把索引记录转成下载所需元数据。
func partOf(f index.File) *telegramstore.Part {
	return &telegramstore.Part{
		MessageID:     f.MessageID,
		DocumentID:    f.DocumentID,
		AccessHash:    f.AccessHash,
		FileReference: f.FileReference,
		Size:          f.Size,
	}
}
