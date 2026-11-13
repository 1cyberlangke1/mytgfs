package dav

// WebDAV 适配层：把频道内容暴露成标准 WebDAV 文件系统。
//
// 读按 1MB 分块按需拉取，写先落临时文件、关闭时整份上传，
// 目录、改名、删除直接操作索引和消息。

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"hash"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/webdav"

	"mytgfs/internal/i18n"
	"mytgfs/internal/index"
	"mytgfs/internal/queue"
	tgsync "mytgfs/internal/sync"
	"mytgfs/internal/telegramstore"
	"mytgfs/internal/upload"
)

// Store 是 WebDAV 层需要的存储能力。
type Store interface {
	ReadRange(ctx context.Context, p *telegramstore.Part, offset int64, length int, progress func(done int64)) ([]byte, error)
	Upload(ctx context.Context, r io.Reader, size int64, name, mime, caption string) (*telegramstore.Part, error)
	// UploadResumable 按内容 key 断点续传，中断后能跳过已上传分片
	UploadResumable(ctx context.Context, r io.ReaderAt, size int64, key, name, mime, caption string) (*telegramstore.Part, error)
	// Refresh 按消息 ID 重新拉取 document 元数据（file_reference 过期时用）
	Refresh(ctx context.Context, messageID int) (*telegramstore.Part, error)
	// Copy 引用已有 document 发一条新消息，不重传字节（写时去重用）
	Copy(ctx context.Context, src *telegramstore.Part, name, mime, caption string) (*telegramstore.Part, error)
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
	// tr 记录正在下载的文件进度；键是卷内路径
	trMu sync.Mutex
	tr   map[string]*Transfer
}

// Transfer 是一个正在下载的文件的进度快照。
//
// Path 是卷内绝对路径；Done 是已从 Telegram 取回的字节数；Total 是文件大小。
type Transfer struct {
	Path  string `json:"path"`
	Done  int64  `json:"done"`
	Total int64  `json:"total"`
}

// transferMinSize 是进入进度列表的最小文件体积。
//
// 小于这个值的文件通常一个分块就读完，显示进度只会让面板闪一下。
const transferMinSize = 1 << 20

// track 记录一次下载进度。
//
// 输入：name 卷内路径；done 已取回字节；total 文件总大小
// 输出：无
// 预期：小于 transferMinSize 或已取完时移除记录；done 只增不减
func (d *FileSystem) track(name string, done, total int64) {
	if total < transferMinSize {
		return
	}
	if done > total {
		done = total
	}
	d.trMu.Lock()
	defer d.trMu.Unlock()
	if done >= total {
		delete(d.tr, name)
		return
	}
	if d.tr == nil {
		d.tr = make(map[string]*Transfer)
	}
	t := d.tr[name]
	if t == nil {
		d.tr[name] = &Transfer{Path: name, Done: done, Total: total}
		return
	}
	if done > t.Done {
		t.Done = done
	}
}

// untrack 移除一条下载进度。
func (d *FileSystem) untrack(name string) {
	d.trMu.Lock()
	delete(d.tr, name)
	d.trMu.Unlock()
}

// Transfers 返回当前下载进度快照，按路径排序。
func (d *FileSystem) Transfers() []Transfer {
	d.trMu.Lock()
	defer d.trMu.Unlock()
	out := make([]Transfer, 0, len(d.tr))
	for _, t := range d.tr {
		out = append(out, *t)
	}
	slices.SortFunc(out, func(a, b Transfer) int { return cmp.Compare(a.Path, b.Path) })
	return out
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

// ResumePending 重启后按 pending 表补传未提交的文件。
//
// 输入：无
// 输出：error（索引读写错误）；单条上传失败只记日志、保留账本下次再试
// 预期：只处理本程序记下的 pending 记录；临时文件缺失时清账不上传；
//
//	成功后清账并删临时文件；绝不扫描或删除频道里任何别的消息。
func (d *FileSystem) ResumePending() error {
	rows, err := d.idx.ListPending(d.ctx)
	if err != nil {
		return err
	}
	for _, p := range rows {
		tmp, err := os.Open(p.Temp)
		if err != nil {
			// 临时文件已被清扫或损坏：无法恢复，只清账
			_ = d.idx.DeletePending(d.ctx, p.Path)
			continue
		}
		st, statErr := tmp.Stat()
		if statErr != nil || st.Size() != p.Size {
			_ = tmp.Close()
			_ = d.idx.DeletePending(d.ctx, p.Path)
			_ = os.Remove(p.Temp)
			continue
		}
		// 补传内容摘要按临时文件实际内容计算，作为断点键与去重依据
		h := sha256.New()
		_, copyErr := io.Copy(h, tmp)
		if copyErr != nil {
			_ = tmp.Close()
			continue
		}
		sha := hex.EncodeToString(h.Sum(nil))
		helper := &file{d: d, name: p.Path}
		// 上次上传已成功（消息号已记下）但索引没写成：只补索引，不重传整份内容。
		// 摘要不一致说明临时文件已被新内容覆盖，旧记账作废，走正常上传。
		done, hasDone, derr := d.idx.GetPendingDone(d.ctx, p.Path)
		if derr == nil && hasDone && done.MessageID > 0 && done.Sha256 == sha {
			perr := helper.record(d.ctx, p.Path, sha, &telegramstore.Part{
				MessageID:     done.MessageID,
				DocumentID:    done.DocumentID,
				AccessHash:    done.AccessHash,
				FileReference: done.FileReference,
				Size:          done.Size,
			})
			if perr != nil {
				slog.Warn(i18n.T("log.pending_resume_failed", p.Path, perr), "path", p.Path, "err", perr)
				_ = tmp.Close()
				continue
			}
			_ = tmp.Close()
			_ = d.idx.DeletePending(d.ctx, p.Path)
			_ = os.Remove(p.Temp)
			continue
		}
		if hasDone {
			// 内容已变化：旧记账与本次提交无关，清掉避免下次误用
			_ = d.idx.DeletePendingDone(d.ctx, p.Path)
		}
		if err := helper.commit(d.ctx, tmp, p.Path, sha); err != nil {
			slog.Warn(i18n.T("log.pending_resume_failed", p.Path, err), "path", p.Path, "err", err)
			_ = tmp.Close()
			continue
		}
		_ = tmp.Close()
		_ = d.idx.DeletePending(d.ctx, p.Path)
		_ = os.Remove(p.Temp)
	}
	return nil
}

// Mkdir 创建目录。
func (d *FileSystem) Mkdir(_ context.Context, name string, _ os.FileMode) error {
	name = clean(name)
	if name == "/" {
		return os.ErrExist
	}
	return d.idx.PutDir(d.ctx, name)
}

// Search 在索引里递归搜索文件名。
//
// 输入：dir 起点目录；query 大小写不敏感子串；limit 命中上限
// 输出：命中项，目录在前、同类型按路径升序
// 预期：只读本地索引，不访问 Telegram
func (d *FileSystem) Search(ctx context.Context, dir, query string, limit int) ([]index.SearchHit, error) {
	return d.idx.Search(ctx, dir, query, limit)
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
		return d.removeTree(name)
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

// removeTree 递归删除目录：先删子树里的消息，再清索引与目录记录。
//
// 输入：dir 卷内目录路径（已 clean，且不是根）
// 输出：error
// 预期：一次 RPC 批量删掉子文件消息；远端删除失败即返回，保留索引便于重试；
// 成功后子文件索引与所有相关目录记录都清空。
func (d *FileSystem) removeTree(dir string) error {
	files, err := d.idx.ListPrefix(d.ctx, dir)
	if err != nil {
		return err
	}
	if len(files) > 0 {
		ids := make([]int, 0, len(files))
		for _, f := range files {
			ids = append(ids, f.MessageID)
		}
		if err := d.store.Delete(d.ctx, ids...); err != nil {
			return err
		}
		for _, f := range files {
			if err := d.idx.Delete(d.ctx, f.Path); err != nil {
				return err
			}
		}
	}
	dirs, err := d.idx.Dirs(d.ctx, dir)
	if err != nil {
		return err
	}
	for _, p := range dirs {
		if err := d.idx.DeleteDir(d.ctx, p); err != nil {
			return err
		}
	}
	return nil
}

// RemoveDir 删除目录记录。
//
// 输入：name 卷内目录路径
// 输出：error
// 预期：
//   - 与 RemoveAll 的区别：不依赖「目录现在还存在」的推断，
//     子项删光之后目录在索引里已经不可推导，仍能把记录删掉
//   - 不递归删内容，内容由调用方（fm）按 WebDAV 语义处理
func (d *FileSystem) RemoveDir(ctx context.Context, name string) error {
	name = clean(name)
	if name == "/" {
		return errors.New(i18n.T("err.dav_root_delete"))
	}
	return d.idx.DeleteDir(ctx, name)
}

// Rename 改名：文件的 caption 跟着改，索引同步迁移；目录则整棵子树一起迁移。
func (d *FileSystem) Rename(_ context.Context, oldName, newName string) error {
	oldName, newName = clean(oldName), clean(newName)
	if oldName == newName {
		return nil
	}
	if oldName == "/" {
		return errors.New(i18n.T("err.dav_root_rename"))
	}
	if d.isDir(oldName) {
		return d.renameDir(oldName, newName)
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

// renameDir 把目录整棵子树迁到新路径。
//
// 输入：oldName 已 clean 的旧目录；newName 新目录
// 输出：error
// 预期：逐文件改 caption 并迁移索引行，再迁移显式目录记录；
// 目标已存在或移进自身子树时拒绝，拒绝时不做任何改动。
func (d *FileSystem) renameDir(oldName, newName string) error {
	// 移进自己的子树会形成环
	if strings.HasPrefix(newName+"/", oldName+"/") {
		return errors.New(i18n.T("err.dav_dir_into_self"))
	}
	// 目标已存在时拒绝，避免覆盖已有文件或目录
	if ok, err := d.idx.IsDir(d.ctx, newName); err != nil {
		return err
	} else if ok {
		return os.ErrExist
	}
	if _, ok, err := d.idx.Get(d.ctx, newName); err != nil {
		return err
	} else if ok {
		return os.ErrExist
	}

	files, err := d.idx.ListPrefix(d.ctx, oldName)
	if err != nil {
		return err
	}
	for _, f := range files {
		np := newName + strings.TrimPrefix(f.Path, oldName)
		// 先改远端消息说明，再迁本地索引；中途失败时重建索引仍能按 caption 恢复
		if err := d.store.Rename(d.ctx, f.MessageID, tgsync.EncodeCaptionHash(np, f.Sha256)); err != nil {
			return err
		}
		if err := d.idx.Delete(d.ctx, f.Path); err != nil {
			return err
		}
		f.Path = np
		if err := d.idx.Replace(d.ctx, f); err != nil {
			return err
		}
	}
	dirs, err := d.idx.Dirs(d.ctx, oldName)
	if err != nil {
		return err
	}
	for _, p := range dirs {
		if err := d.idx.PutDir(d.ctx, newName+strings.TrimPrefix(p, oldName)); err != nil {
			return err
		}
		if err := d.idx.DeleteDir(d.ctx, p); err != nil {
			return err
		}
	}
	// 派生目录（没有显式记录）也要保证新路径成立
	return d.idx.PutDir(d.ctx, newName)
}

// CopyPrefix 用引用复制实现 WebDAV COPY：源文件只发一条引用消息，不传输字节。
//
// 输入：src 源路径；dst 目标路径；overwrite 目标已存在时是否替换；
// depth <0 表示整棵树，0 表示目录只建壳
// 输出：error
// 预期：
//   - 源不存在返回 os.ErrNotExist；目标存在且不允许覆盖返回 os.ErrExist
//   - 目标父目录必须已存在，否则同样按不存在处理
//   - 文件走 store.Copy 发引用消息；目录递归复制子文件与目录记录
func (d *FileSystem) CopyPrefix(ctx context.Context, src, dst string, overwrite bool, depth int) error {
	src, dst = clean(src), clean(dst)
	if src == "/" || dst == "/" {
		return errors.New(i18n.T("err.dav_root_copy"))
	}
	if src == dst {
		return errors.New(i18n.T("err.dav_copy_same"))
	}
	if strings.HasPrefix(dst+"/", src+"/") {
		return errors.New(i18n.T("err.dav_copy_into_self"))
	}
	if parent := path.Dir(dst); parent != "/" && !d.isDir(parent) {
		return os.ErrNotExist
	}
	if d.exists(dst) {
		if !overwrite {
			return os.ErrExist
		}
		if err := d.RemoveAll(ctx, dst); err != nil {
			return err
		}
	}
	f, ok, err := d.idx.Get(d.ctx, src)
	if err != nil {
		return err
	}
	if ok {
		return d.copyFile(ctx, f, dst)
	}
	if d.isDir(src) {
		return d.copyTree(ctx, src, dst, depth)
	}
	return os.ErrNotExist
}

// exists 判断路径是否已是文件或目录。
func (d *FileSystem) exists(p string) bool {
	if f, ok, err := d.idx.Get(d.ctx, p); err == nil && ok && f.MessageID > 0 {
		return true
	}
	return d.isDir(p)
}

// copyFile 复制单个文件：发引用消息并登记索引。
func (d *FileSystem) copyFile(ctx context.Context, f index.File, dst string) error {
	part, err := d.store.Copy(ctx, partOf(f), path.Base(dst), "application/octet-stream",
		tgsync.EncodeCaptionHash(dst, f.Sha256))
	if err != nil {
		return err
	}
	return d.idx.Replace(ctx, index.File{
		Path:          dst,
		MessageID:     part.MessageID,
		DocumentID:    part.DocumentID,
		AccessHash:    part.AccessHash,
		FileReference: part.FileReference,
		Size:          part.Size,
		Mtime:         time.Now().Unix(),
		Sha256:        f.Sha256,
	})
}

// copyTree 复制整棵目录：先建目录记录，再逐文件引用复制。
func (d *FileSystem) copyTree(ctx context.Context, src, dst string, depth int) error {
	if err := d.idx.PutDir(ctx, dst); err != nil {
		return err
	}
	if depth == 0 {
		return nil
	}
	dirs, err := d.idx.Dirs(ctx, src)
	if err != nil {
		return err
	}
	for _, p := range dirs {
		if p == src {
			continue
		}
		if err := d.idx.PutDir(ctx, dst+strings.TrimPrefix(p, src)); err != nil {
			return err
		}
	}
	files, err := d.idx.ListPrefix(ctx, src)
	if err != nil {
		return err
	}
	for _, f := range files {
		if err := d.copyFile(ctx, f, dst+strings.TrimPrefix(f.Path, src)); err != nil {
			return err
		}
	}
	return nil
}

// copyFileSystem 是 COPY 优化要求的能力。
type copyFileSystem interface {
	webdav.FileSystem
	CopyPrefix(ctx context.Context, src, dst string, overwrite bool, depth int) error
}

// copyHandler 把 COPY 换成引用复制，其余请求交给标准 handler。
type copyHandler struct {
	base *webdav.Handler
	fs   copyFileSystem
}

// NewHandler 包装 webdav.Handler，让 COPY 走引用复用。
//
// 输入：fs 文件系统；ls 锁系统（nil 时新建内存锁）
// 输出：http.Handler
// 预期：
//   - COPY 在本地索引可解析时零字节完成，返回 201（新建）或 204（覆盖）
//   - 其它方法、缺少 Destination、跨主机目标原样交给标准 handler
func NewHandler(fs webdav.FileSystem, ls webdav.LockSystem) http.Handler {
	if ls == nil {
		ls = webdav.NewMemLS()
	}
	base := &webdav.Handler{Prefix: "/", FileSystem: fs, LockSystem: ls}
	cfs, ok := fs.(copyFileSystem)
	if !ok {
		return base
	}
	return &copyHandler{base: base, fs: cfs}
}

func (h *copyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != "COPY" {
		h.base.ServeHTTP(w, r)
		return
	}
	u, err := url.Parse(r.Header.Get("Destination"))
	if err != nil || u.Path == "" || (u.Host != "" && u.Host != r.Host) {
		h.base.ServeHTTP(w, r)
		return
	}
	src, dst := clean(r.URL.Path), clean(u.Path)
	overwrite := r.Header.Get("Overwrite") != "F"
	depth := -1
	if r.Header.Get("Depth") == "0" {
		depth = 0
	}
	existed := false
	if _, serr := h.base.FileSystem.Stat(r.Context(), dst); serr == nil {
		existed = true
	}
	if cerr := h.fs.CopyPrefix(r.Context(), src, dst, overwrite, depth); cerr != nil {
		switch {
		case errors.Is(cerr, os.ErrNotExist):
			w.WriteHeader(http.StatusNotFound)
		case errors.Is(cerr, os.ErrExist):
			w.WriteHeader(http.StatusPreconditionFailed)
		default:
			w.WriteHeader(http.StatusForbidden)
		}
		return
	}
	if existed {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.WriteHeader(http.StatusCreated)
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
	f.d.untrack(f.name)
	if f.write && f.tmp != nil {
		name := f.tmp.Name()
		_ = f.tmp.Close()
		f.tmp = nil
		return os.Remove(name)
	}
	return nil
}

// commit 把一份完整内容上传到频道并登记索引。
//
// 输入：ctx；tmp 已写完的临时文件；name 卷内路径；sha 内容摘要
// 输出：error
// 预期：上传或索引登记失败时保留临时文件与 pending 记录，供重启补传；
//
//	只处理本程序记下的文件，不触碰频道里任何别的消息。
func (f *file) commit(ctx context.Context, tmp *os.File, name, sha string) error {
	st, err := tmp.Stat()
	if err != nil {
		return err
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return err
	}
	caption := tgsync.EncodeCaptionHash(name, sha)
	req := upload.Request{
		Path:    name,
		Reader:  tmp,
		Size:    st.Size(),
		SHA:     sha,
		Caption: caption,
	}
	// 有队列时串行上传：批量写入不会互相抢限流额度
	if f.d.q != nil {
		req.Queue = f.d.q
	}
	part, _, uerr := upload.Write(ctx, f.d.idx, f.d.store, req)
	if uerr != nil {
		return uerr
	}
	if part == nil {
		return errors.New(i18n.T("err.upload_no_result"))
	}
	return f.record(ctx, name, sha, part)
}

// record 把上传结果登记到索引，并清理被覆盖的旧消息。
//
// 输入：ctx；name 卷内路径；sha 内容摘要；part 已上传成功的消息
// 输出：error
// 预期：先写 pending_done 记账再写 files，索引失败时记账仍在，
// 重启 ResumePending 只需补一次索引而不重传整份内容；
// 索引写成后清掉记账，清账失败只记日志（数据已可用，不能因此再传一遍）。
func (f *file) record(ctx context.Context, name, sha string, part *telegramstore.Part) error {
	if err := f.d.idx.PutPendingDone(ctx, index.PendingDone{
		Path:          name,
		MessageID:     part.MessageID,
		DocumentID:    part.DocumentID,
		AccessHash:    part.AccessHash,
		FileReference: part.FileReference,
		Size:          part.Size,
		Sha256:        sha,
		Mtime:         time.Now().Unix(),
	}); err != nil {
		return err
	}
	// 覆盖写：旧消息要删掉，否则频道里会堆同路径的旧文件消息
	old, hadOld, gerr := f.d.idx.Get(ctx, name)
	if gerr != nil {
		return gerr
	}
	if err := f.d.idx.Put(ctx, index.File{
		Path:          name,
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
		if derr := f.d.store.Delete(ctx, old.MessageID); derr != nil {
			slog.Debug(i18n.T("log.delete_old_msg"), "path", name, "msg", old.MessageID, "err", derr)
		}
	}
	if err := f.d.idx.DeletePendingDone(ctx, name); err != nil {
		slog.Debug(i18n.T("log.cleanup_acked"), "path", name, "err", err)
	}
	return nil
}

// Close 关闭：写模式先记账再提交上传，读模式释放缓冲。
//
// 上传失败时保留账本与临时文件（不 defer 删除），重启由 ResumePending 补传；
// 成功后才删账本与临时文件。
func (f *file) Close() error {
	f.d.untrack(f.name)
	if !f.write || f.tmp == nil {
		return nil
	}
	tmp := f.tmp
	f.tmp = nil
	sha := ""
	if f.sum != nil {
		sha = hex.EncodeToString(f.sum.Sum(nil))
	}
	st, err := tmp.Stat()
	if err != nil {
		_ = tmp.Close()
		return err
	}
	// 提交前先记一笔：上传/登记阶段崩溃也能按账恢复
	if err := f.d.idx.PutPending(f.d.ctx, index.PendingUpload{
		Path: f.name, Temp: tmp.Name(), Size: st.Size(), Mtime: time.Now().Unix(),
	}); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := f.commit(f.d.ctx, tmp, f.name, sha); err != nil {
		// 失败：关句柄但保留磁盘临时文件与 pending 账本，重启补传
		_ = tmp.Close()
		return err
	}
	name := tmp.Name()
	// Windows 下必须先关句柄才能删除文件
	_ = tmp.Close()
	_ = f.d.idx.DeletePending(f.d.ctx, f.name)
	return os.Remove(name)
}

// Read 按需拉取一块再返回。
func (f *file) Read(p []byte) (int, error) {
	if f.dir || f.entry == nil {
		return 0, io.EOF
	}
	if f.off >= f.entry.Size {
		f.d.untrack(f.name)
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
		data, err := f.readRange(f.off, int(length))
		if err != nil {
			// 消息在 Telegram 侧被删了：索引里这条已经没有意义，删掉并按文件不存在处理
			if telegramstore.IsMessageGone(err) {
				slog.Debug(i18n.T("log.remote_gone"), "path", f.name, "msg", f.entry.MessageID)
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

// readRange 读取一块远端数据；file_reference 过期时刷新元数据并重试一次。
//
// 输入：ofst 卷内偏移；length 字节数
// 输出：数据、error
// 预期：刷新成功时把新引用写回索引（Replace 无条件覆盖，绕过 message_id 大小比较），
//
//	刷新失败或二次读取仍失败则原样返回错误；不改动其它消息。
func (f *file) readRange(ofst int64, length int) ([]byte, error) {
	prog := func(done int64) { f.d.track(f.name, done, f.entry.Size) }
	data, err := f.d.store.ReadRange(f.d.ctx, partOf(*f.entry), ofst, length, prog)
	if err == nil {
		return data, nil
	}
	if !telegramstore.IsFileReferenceError(err) {
		return nil, err
	}
	part, rerr := f.d.store.Refresh(f.d.ctx, f.entry.MessageID)
	if rerr != nil {
		return nil, rerr
	}
	updated := *f.entry
	updated.DocumentID = part.DocumentID
	updated.AccessHash = part.AccessHash
	updated.FileReference = part.FileReference
	updated.Size = part.Size
	if perr := f.d.idx.Replace(f.d.ctx, updated); perr != nil {
		return nil, perr
	}
	f.entry = &updated
	return f.d.store.ReadRange(f.d.ctx, partOf(updated), ofst, length, prog)
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
