package vfs

// 跨卷复制：把源卷的文件挂到本卷路径上，不重传字节。
//
// 输入：源卷名、源路径、目标路径、是否覆盖
// 输出：一条引用同一 document 的新消息 + 本地节点登记
// 预期：
//   - 走持久化队列：断网/断电后由队列重放，面板能看到进度与失败原因
//   - 执行体幂等：目标已是同一份内容时直接算完成，不重复发消息

import (
	"context"
	"errors"
	"os"

	"mytgfs/internal/i18n"
	"mytgfs/internal/index"
	"mytgfs/internal/logging"
	"mytgfs/internal/queue"
)

// Peer 是另一个卷的只读视图，跨卷复制只需要索引与存储。
type Peer struct {
	Index *index.Index
	Store Store
	// Release 在复制结束后释放临时打开的索引；同卷复用现有句柄时留空
	Release func()
}

// CopySource 按卷名解析另一个卷。
//
// 输入：卷名
// 输出：该卷的索引与存储；卷不存在时返回错误
// 预期：由装配根注入，vfs 本身不认识配置文件
type CopySource func(volume string) (Peer, error)

// SetCopySource 注入跨卷复制的源卷解析器。
//
// 输入：解析函数
// 输出：无
// 预期：只有接了这一层，排队的复制任务才能在重启后重放
func (d *FileSystem) SetCopySource(fn CopySource) { d.copySource = fn }

// copyJob 跨卷复制所需的最小参数。
//
// SrcVol/SrcPath 在执行时才解析成节点：源文件若在入队后被改名/重传，
// 复制的是当时最新的内容，而不是一份可能已过期的快照。
type copyJob struct {
	SrcVol  string `json:"src_vol"`
	SrcPath string `json:"src_path"`
	Dst     string `json:"dst"`
	Force   bool   `json:"force"`
}

// Copy 把另一个卷的文件复制到本卷路径。
//
// 输入：ctx；srcVol 源卷名；src 源路径；dst 目标路径；force 覆盖已存在目标
// 输出：error；源不存在、目标冲突这类确定性错误直接返回
// 预期：
//   - 先落库再执行：中途断网/断电后队列重放，不会只复制一半
//   - 只发一条新消息，源消息与源卷都不变
func (d *FileSystem) Copy(ctx context.Context, srcVol, src, dst string, force bool) error {
	dst = clean(dst)
	if d.copySource == nil {
		return errors.New(i18n.T("err.cp_unavailable"))
	}
	payload := copyJob{SrcVol: srcVol, SrcPath: clean(src), Dst: dst, Force: force}
	return d.enqueue(ctx, queue.KindCopy, dst, payload)
}

// copyNow 执行一次跨卷复制；已经复制好的直接算完成。
//
// 输入：ctx；p 任务参数
// 输出：error；确定性错误包成 Permanent，避免无限重试
// 预期：
//   - 目标已存在且内容相同 => 幂等成功
//   - 目标已存在且内容不同 => 只有 Force 才替换，否则永久失败
//   - 源 file_reference 失效时由目标 store 用源 store 刷新后重试一次
func (d *FileSystem) copyNow(ctx context.Context, p copyJob) error {
	if d.copySource == nil {
		return queue.Permanent(errors.New(i18n.T("err.cp_unavailable")))
	}
	peer, err := d.copySource(p.SrcVol)
	if err != nil {
		return queue.Permanent(err)
	}
	if peer.Release != nil {
		defer peer.Release()
	}

	src, found, err := peer.Index.NodeByPath(ctx, p.SrcPath)
	if err != nil {
		return err
	}
	if !found {
		return queue.Permanent(errors.New(i18n.T("err.cp_src_missing", p.SrcPath)))
	}
	if src.IsDir {
		return queue.Permanent(errors.New(i18n.T("err.cp_src_is_dir", p.SrcPath)))
	}

	if old, exists, err := d.idx.NodeByPath(ctx, p.Dst); err != nil {
		return err
	} else if exists {
		if old.Sha256 != "" && old.Sha256 == src.Sha256 && old.Size == src.Size {
			return nil
		}
		if !p.Force {
			return queue.Permanent(errors.New(i18n.T("err.cp_dst_exists", p.Dst)))
		}
		// 这里不能走 d.RemoveAll：那是入队操作，同卷串行会把自己堵死
		if rerr := d.removeNow(ctx, old.ID); rerr != nil {
			return rerr
		}
	}

	parent, base, ok, err := d.parentOf(ctx, p.Dst)
	if err != nil {
		return err
	}
	if !ok {
		return queue.Permanent(os.ErrNotExist)
	}
	body, err := nodeBody(base, parent.ID, false, src.Sha256)
	if err != nil {
		return err
	}
	part, refreshed, err := d.store.CopyFrom(ctx, peer.Store, partOf(src), base,
		"application/octet-stream", body)
	if err != nil {
		return err
	}
	// 源引用被刷新时写回源索引，否则下次复制还要再刷一遍
	if refreshed != nil && peer.Index != d.idx {
		updated := src
		updated.DocumentID = refreshed.DocumentID
		updated.AccessHash = refreshed.AccessHash
		updated.FileReference = refreshed.FileReference
		updated.Size = refreshed.Size
		if perr := peer.Index.PutNode(ctx, updated); perr != nil {
			logging.Warn("err.index_read", perr)
		}
	}
	if err := d.idx.PutNode(ctx, index.Node{
		ID: part.MessageID, ParentID: parent.ID, Name: base, Path: p.Dst,
		DocumentID: part.DocumentID, AccessHash: part.AccessHash,
		FileReference: part.FileReference, Size: part.Size, Mtime: src.Mtime,
		Sha256: src.Sha256, Thumb: part.Thumb,
	}); err != nil {
		return err
	}
	d.notify()
	return nil
}
