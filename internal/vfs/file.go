package vfs

// 文件句柄：读、写、提交、列目录。
//
// 输入：File 接口调用
// 输出：字节流、FileInfo、提交结果
// 预期：写入落临时文件，Close 时整份上传，节点消息自述名字与父消息号

import (
	"context"
	"encoding/hex"
	"errors"
	"hash"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"time"

	"mytgfs/internal/i18n"
	"mytgfs/internal/index"
	"mytgfs/internal/telegramstore"
	"mytgfs/internal/upload"
)

type file struct {
	d     *FileSystem
	name  string
	tmp   *os.File
	write bool
	dir   bool
	perm  os.FileMode
	// node 是读模式下打开的文件或目录节点
	node *index.Node

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

// commit 把一份完整内容上传到频道。
//
// 输入：ctx；tmp 已写完的临时文件；name 卷内路径；sha 内容摘要
// 输出：error
// 预期：上传或登记失败时保留临时文件与 pending 记录，供重启补传；
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
	parent, base, ok, err := f.d.parentOf(ctx, name)
	if err != nil {
		return err
	}
	if !ok {
		return os.ErrNotExist
	}
	// 节点消息正文在上传时就写死 name+parent：文件消息自己描述自己。
	body, err := nodeBody(base, parent.ID, false, sha)
	if err != nil {
		return err
	}
	req := upload.Request{
		Path:    name,
		Reader:  tmp,
		Size:    st.Size(),
		SHA:     sha,
		Caption: body,
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

// record 把上传结果登记到本地节点表，并清理被覆盖的旧消息。
//
// 输入：ctx；name 卷内路径；sha 内容摘要；part 已上传成功的消息
// 输出：error
// 预期：
//   - 先记账再写本地节点；中途失败由 pending 账本补
//   - 覆盖写时删掉旧节点的消息，避免频道里堆同一路径的旧内容
func (f *file) record(ctx context.Context, name, sha string, part *telegramstore.Part) error {
	name = clean(name)
	parent, base, ok, err := f.d.parentOf(ctx, name)
	if err != nil {
		return err
	}
	if !ok {
		return os.ErrNotExist
	}
	if err := f.d.idx.PutPendingDone(ctx, index.PendingDone{
		Path:          name,
		MessageID:     part.MessageID,
		DocumentID:    part.DocumentID,
		AccessHash:    part.AccessHash,
		FileReference: part.FileReference,
		Size:          part.Size,
		Sha256:        sha,
		Mtime:         nowUnix(),
	}); err != nil {
		return err
	}
	old, hadOld, gerr := f.d.idx.NodeByPath(ctx, name)
	if gerr != nil {
		return gerr
	}
	// 先删旧的本地节点，再写新的：nodes 表对 (parent_id, name) 唯一，
	// 同路径覆盖写若先插新行会撞约束。
	if hadOld && old.ID > 0 && old.ID != part.MessageID {
		if derr := f.d.idx.DeleteNode(ctx, old.ID); derr != nil {
			return derr
		}
	}
	if err := f.d.idx.PutNode(ctx, index.Node{
		ID:            part.MessageID,
		ParentID:      parent.ID,
		Name:          base,
		Path:          name,
		DocumentID:    part.DocumentID,
		AccessHash:    part.AccessHash,
		FileReference: part.FileReference,
		Size:          part.Size,
		Mtime:         nowUnix(),
		Sha256:        sha,
	}); err != nil {
		return err
	}
	// 远端旧消息最后删：删失败只留下孤儿消息，本地视图已经正确
	if hadOld && old.ID > 0 && old.ID != part.MessageID {
		if derr := f.d.store.Delete(ctx, old.ID); derr != nil {
			slog.Debug(i18n.T("log.delete_old_msg"), "path", name, "msg", old.ID, "err", derr)
		}
	}
	if err := f.d.idx.DeletePendingDone(ctx, name); err != nil {
		slog.Debug(i18n.T("log.cleanup_acked"), "path", name, "err", err)
	}
	f.d.notify()
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
		Path: f.name, Temp: tmp.Name(), Size: st.Size(), Mtime: nowUnix(),
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
	if f.dir || f.node == nil {
		return 0, io.EOF
	}
	if f.off >= f.node.Size {
		f.d.untrack(f.name)
		return 0, io.EOF
	}
	if f.buf == nil || f.off < f.base || f.off >= f.base+int64(len(f.buf)) {
		length := f.d.chunk
		if length <= 0 {
			length = 1 << 20
		}
		if f.off+length > f.node.Size {
			length = f.node.Size - f.off
		}
		data, err := f.readRange(f.off, int(length))
		if err != nil {
			// 消息在 Telegram 侧被删了：本地这条已经没有意义，删掉并按文件不存在处理
			if telegramstore.IsMessageGone(err) {
				slog.Debug(i18n.T("log.remote_gone"), "path", f.name, "msg", f.node.ID)
				_ = f.d.idx.DeleteNode(f.d.ctx, f.node.ID)
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
// 预期：刷新成功时把新引用写回本地节点，
//
//	刷新失败或二次读取仍失败则原样返回错误；不改动其它消息。
func (f *file) readRange(ofst int64, length int) ([]byte, error) {
	prog := func(done int64) { f.d.track(f.name, done, f.node.Size) }
	data, err := f.d.store.ReadRange(f.d.ctx, partOf(*f.node), ofst, length, prog)
	if err == nil {
		return data, nil
	}
	if !telegramstore.IsFileReferenceError(err) {
		return nil, err
	}
	part, rerr := f.d.store.Refresh(f.d.ctx, f.node.ID)
	if rerr != nil {
		return nil, rerr
	}
	updated := *f.node
	updated.DocumentID = part.DocumentID
	updated.AccessHash = part.AccessHash
	updated.FileReference = part.FileReference
	updated.Size = part.Size
	if perr := f.d.idx.PutNode(f.d.ctx, updated); perr != nil {
		return nil, perr
	}
	f.node = &updated
	return f.d.store.ReadRange(f.d.ctx, partOf(updated), ofst, length, prog)
}

// Seek 只支持顺序定位，跳转后重新拉块。
func (f *file) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
	case io.SeekCurrent:
		offset += f.off
	case io.SeekEnd:
		if f.node == nil {
			return 0, errors.New(i18n.T("err.dav_unknown_size"))
		}
		offset += f.node.Size
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
	if !f.dir || f.node == nil {
		return nil, errors.New(i18n.T("err.dav_not_dir"))
	}
	children, err := f.d.idx.Children(f.d.ctx, f.node.ID)
	if err != nil {
		return nil, err
	}
	out := make([]fs.FileInfo, 0, len(children))
	for _, c := range children {
		if c.IsDir {
			out = append(out, info{name: c.Name, dir: true, mod: unixTime(c.Mtime)})
			continue
		}
		out = append(out, info{name: c.Name, size: c.Size, mod: unixTime(c.Mtime)})
	}
	return out, nil
}

// Stat 返回自身信息。
func (f *file) Stat() (fs.FileInfo, error) {
	if f.node == nil {
		return f.tmp.Stat()
	}
	if f.node.IsDir {
		return info{name: path.Base(f.name), dir: true, mod: unixTime(f.node.Mtime)}, nil
	}
	return info{name: path.Base(f.name), size: f.node.Size, mod: unixTime(f.node.Mtime)}, nil
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
