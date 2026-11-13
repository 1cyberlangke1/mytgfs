package dav

// WebDAV 适配层：把频道内容暴露成标准 WebDAV 文件系统。
//
// 读按 1MB 分块按需拉取，写先落临时文件、关闭时整份上传，
// 目录、改名、删除直接操作索引和消息。

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"hash"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/webdav"

	"mytgfs/internal/i18n"
	"mytgfs/internal/index"
	"mytgfs/internal/queue"
	tgsync "mytgfs/internal/sync"
	"mytgfs/internal/telegramstore"
)

// Store 是 WebDAV 层需要的存储能力。
type Store interface {
	ReadRange(ctx context.Context, p *telegramstore.Part, offset int64, length int, progress func(done int64)) ([]byte, error)
	Upload(ctx context.Context, r io.Reader, size int64, name, mime, caption string) (*telegramstore.Part, error)
	// UploadResumable 按内容 key 断点续传，中断后能跳过已上传分片
	UploadResumable(ctx context.Context, r io.ReaderAt, size int64, key, name, mime, caption string) (*telegramstore.Part, error)
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
	// q 可选：非空时上传走串行队列，避免批量写入把 Telegram 打出 FLOOD_WAIT
	q *queue.Queue
	// tmpDir 是上传临时文件目录；空表示系统临时目录
	tmpDir string
	// tmpOnce 保证每个进程只扫一次旧临时文件
	tmpOnce sync.Once
}

// 上传临时文件的命名与保留时长。
//
// 异常中断（关标签页、拔网线、杀进程）时 Close 不会被调用，
// 临时文件只能靠下次上传时按时间清理。
const (
	tempPattern = ".mytgfs-dav-*"
	tempMaxAge  = 24 * time.Hour
)

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

// SetQueue 挂上串行队列。
//
// 输入：q 队列，可为 nil（直接上传）
// 输出：无
// 预期：只有写入路径受影响，读取不变
func (d *FileSystem) SetQueue(q *queue.Queue) { d.q = q }

// SetTempDir 指定上传临时文件目录。
//
// 输入：dir 目录，空表示系统临时目录
// 输出：无
// 预期：目录不存在时首次写入自动创建
func (d *FileSystem) SetTempDir(dir string) { d.tmpDir = dir }

// newTemp 创建上传临时文件，并在首次使用时清理上次残留。
//
// 输入：无
// 输出：已打开的临时文件；error
// 预期：只删 tempMaxAge 以前、名字匹配 tempPattern 的普通文件
func (d *FileSystem) newTemp() (*os.File, error) {
	dir := d.tmpDir
	if dir == "" {
		dir = os.TempDir()
	} else if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	d.tmpOnce.Do(func() {
		n, err := SweepTemp(dir, tempMaxAge)
		if err != nil {
			slog.Warn(i18n.T("log.tmp_sweep_failed"), "dir", dir, "err", err)
			return
		}
		if n > 0 {
			slog.Info(i18n.T("log.tmp_sweep"), "count", n, "dir", dir)
		}
	})
	return os.CreateTemp(dir, tempPattern)
}

// SweepTemp 删除目录里过期的 mytgfs 上传临时文件。
//
// 输入：dir 目录；maxAge 保留时长
// 输出：删除的数量；error
// 预期：目录不存在视为没有残留；只处理名字以 .mytgfs-dav- 开头的普通文件
func SweepTemp(dir string, maxAge time.Duration) (int, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	cutoff := time.Now().Add(-maxAge)
	n := 0
	for _, e := range ents {
		if e.IsDir() || !strings.HasPrefix(e.Name(), ".mytgfs-dav-") {
			continue
		}
		info, ierr := e.Info()
		if ierr != nil || info.ModTime().After(cutoff) {
			continue
		}
		if rerr := os.Remove(filepath.Join(dir, e.Name())); rerr == nil {
			n++
		}
	}
	return n, nil
}

// Queue 返回当前队列，nil 表示未启用。
func (d *FileSystem) Queue() *queue.Queue { return d.q }

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
		tmp, err := d.newTemp()
		if err != nil {
			return nil, err
		}
		return &file{d: d, name: name, tmp: tmp, write: true, perm: perm, sum: sha256.New()}, nil
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
		return errors.New(i18n.T("err.dav_root_delete"))
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
		return errors.New(i18n.T("err.dav_dir_rename"))
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
	// sum 在写模式下累计内容 sha256，作为断点键与去重依据
	sum hash.Hash
}

// Abort 丢弃写模式的临时文件，不提交上传。
//
// 输入：无
// 输出：error（删除临时文件失败）
// 预期：只用于写入中途出错的分支；正常结束走 Close。
// 调用后 Close 变成空操作，避免把半份内容传上去。
func (f *file) Abort() error {
	if f.write && f.tmp != nil {
		name := f.tmp.Name()
		_ = f.tmp.Close()
		f.tmp = nil
		return os.Remove(name)
	}
	return nil
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
		sha := ""
		if f.sum != nil {
			sha = hex.EncodeToString(f.sum.Sum(nil))
		}
		caption := tgsync.EncodeCaptionHash(f.name, sha)
		var part *telegramstore.Part
		upload := func(ctx context.Context) error {
			p, uerr := f.d.store.UploadResumable(ctx, f.tmp, st.Size(), sha,
				path.Base(f.name), "application/octet-stream", caption)
			if uerr != nil {
				return uerr
			}
			part = p
			return nil
		}
		// 有队列时串行上传：批量写入不会互相抢限流额度
		if f.d.q != nil {
			if err := f.d.q.Do(f.d.ctx, f.name, upload); err != nil {
				return err
			}
		} else if err := upload(f.d.ctx); err != nil {
			return err
		}
		if part == nil {
			return errors.New(i18n.T("err.upload_no_result"))
		}
		// 覆盖写：旧消息要删掉，否则频道里会堆同路径的孤儿消息
		old, hadOld, gerr := f.d.idx.Get(f.d.ctx, f.name)
		if gerr != nil {
			return gerr
		}
		if err := f.d.idx.Put(f.d.ctx, index.File{
			Path:          f.name,
			MessageID:     part.MessageID,
			DocumentID:    part.DocumentID,
			AccessHash:    part.AccessHash,
			FileReference: part.FileReference,
			Size:          part.Size,
			Mtime:         time.Now().Unix(),
			Sha256:        sha,
		}); err != nil {
			return err
		}
		if hadOld && old.MessageID > 0 && old.MessageID != part.MessageID {
			if derr := f.d.store.Delete(f.d.ctx, old.MessageID); derr != nil {
				slog.Debug("删除被覆盖的旧消息失败", "path", f.name, "msg", old.MessageID, "err", derr)
			}
		}
		return nil
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
			// 消息在 Telegram 侧被删了：索引里这条已经没有意义，删掉并按文件不存在处理
			if telegramstore.IsMessageGone(err) {
				slog.Debug("远端消息已被删除，清理索引", "path", f.name, "msg", f.entry.MessageID)
				_ = f.d.idx.Delete(f.d.ctx, f.name)
				return 0, os.ErrNotExist
			}
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
			return 0, errors.New(i18n.T("err.dav_unknown_size"))
		}
		offset += f.entry.Size
	default:
		return 0, errors.New(i18n.T("err.dav_whence"))
	}
	if offset < 0 {
		return 0, errors.New(i18n.T("err.dav_negative_offset"))
	}
	f.off = offset
	f.buf = nil
	return offset, nil
}

// Write 写临时文件，同时累计内容 sha256。
func (f *file) Write(p []byte) (int, error) {
	if !f.write {
		return 0, errors.New(i18n.T("err.dav_not_write"))
	}
	n, err := f.tmp.Write(p)
	if n > 0 && f.sum != nil {
		_, _ = f.sum.Write(p[:n])
	}
	return n, err
}

// Readdir 列出目录项。
func (f *file) Readdir(count int) ([]fs.FileInfo, error) {
	if !f.dir {
		return nil, errors.New(i18n.T("err.dav_not_dir"))
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
