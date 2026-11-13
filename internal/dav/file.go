package dav

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
	tgsync "mytgfs/internal/sync"
	"mytgfs/internal/telegramstore"
	"mytgfs/internal/upload"
)

// WebDAV 文件句柄：读、写、提交、列目录。
//
// 输入：webdav.File 接口调用
// 输出：字节流、FileInfo、提交结果
// 预期：写入落临时文件，Close 时提交到频道并写索引

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
