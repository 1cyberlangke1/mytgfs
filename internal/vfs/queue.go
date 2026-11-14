package vfs

// 把会改动频道的操作变成可重放的任务。
//
// 输入：vfs 的写接口（Mkdir/Rename/RemoveAll/文件提交）
// 输出：持久化到本地库的任务，执行完才返回
// 预期：
//   - 任务体只描述「做什么」，进程重启后能按同样的参数重跑
//   - 执行函数自己做幂等判断：已经改好的直接算成功，不重复发消息
//   - 网络断、429、5xx 由队列重试，调用方取消只影响本次等待

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"mytgfs/internal/index"
	"mytgfs/internal/queue"
	"mytgfs/internal/telegramstore"
)

// 任务类型（与 queue.Kind* 对应，单独写常量避免拼错）。
const (
	jobMkdir  = queue.KindMkdir
	jobRename = queue.KindRename
	jobRemove = queue.KindRemove
	jobUpload = queue.KindUpload
)

// mkdirJob 建目录所需的最小参数。
type mkdirJob struct {
	Parent int    `json:"parent"`
	Name   string `json:"name"`
}

// renameJob 改名/移动所需参数：节点号、新父号、新名字。
type renameJob struct {
	ID     int    `json:"id"`
	Parent int    `json:"parent"`
	Name   string `json:"name"`
}

// removeJob 删除子树所需的根节点号。
type removeJob struct {
	ID int `json:"id"`
}

// uploadJob 提交一份已落盘的内容。
//
// Parent/Base/Body 在上传时定稿：即使父目录之后改名，重放也能写对。
type uploadJob struct {
	Path   string `json:"path"`
	Temp   string `json:"temp"`
	Size   int64  `json:"size"`
	Sha    string `json:"sha"`
	Base   string `json:"base"`
	Body   string `json:"body"`
	Parent int    `json:"parent"`
}

// AttachQueue 为卷创建持久化队列并启动工作协程。
//
// 输入：ctx 生命周期（进程退出时取消）
// 输出：已启动的队列
// 预期：同一卷只调用一次；队列与索引共用一个 SQLite 库，崩溃后自动重排
func (d *FileSystem) AttachQueue(ctx context.Context) (*queue.Queue, error) {
	// 任务入队/开始/结束都推一次状态，面板的操作队列才能实时走完队列→执行→完成
	q, err := queue.Open(d.idx.DB(), 0, d.notify)
	if err != nil {
		return nil, err
	}
	q.SetHandler(d.handleJob)
	d.q = q
	q.Start(ctx)
	return q, nil
}

// Close 停止队列工作协程。
//
// 输入：无
// 输出：无
// 预期：必须在关闭索引之前调用，否则在跑任务会踩到已关闭的连接
func (d *FileSystem) Close() {
	if d.q != nil {
		d.q.Close()
	}
}

// handleJob 按 Kind 分发任务。
//
// 输入：ctx；job 任务
// 输出：执行错误；nil 表示完成
// 预期：未知类型直接失败，不静默丢弃
func (d *FileSystem) handleJob(ctx context.Context, job queue.Job) error {
	switch job.Kind {
	case jobMkdir:
		var p mkdirJob
		if err := json.Unmarshal([]byte(job.Payload), &p); err != nil {
			return err
		}
		return d.mkdirNow(ctx, p.Parent, p.Name)
	case jobRename:
		var p renameJob
		if err := json.Unmarshal([]byte(job.Payload), &p); err != nil {
			return err
		}
		return d.renameNow(ctx, p.ID, p.Parent, p.Name)
	case jobRemove:
		var p removeJob
		if err := json.Unmarshal([]byte(job.Payload), &p); err != nil {
			return err
		}
		return d.removeNow(ctx, p.ID)
	case jobUpload:
		var p uploadJob
		if err := json.Unmarshal([]byte(job.Payload), &p); err != nil {
			return err
		}
		return d.uploadNow(ctx, p)
	default:
		return fmt.Errorf("%s: %s", job.Volume, job.Kind)
	}
}

// mustJSON 把任务参数编码成字符串；结构体编码不会失败。
func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// enqueue 落库并等待任务结束。
//
// 输入：ctx；kind 类型；name 展示名；payload 参数结构
// 输出：error
// 预期：队列未挂时直接执行同一份逻辑（测试与 CLI 单跑）
func (d *FileSystem) enqueue(ctx context.Context, kind, name string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if d.q == nil {
		return d.handleJob(ctx, queue.Job{
			Volume: d.volume, Kind: kind, Name: name, Payload: string(raw),
		})
	}
	id, err := d.q.EnqueueOnce(ctx, queue.Job{
		Volume: d.volume, Kind: kind, Name: name, Payload: string(raw),
	})
	if err != nil {
		return err
	}
	return d.q.Wait(ctx, id)
}

// ---- 幂等执行体 ----

// mkdirNow 发目录消息并登记；同名已存在视为已完成。
func (d *FileSystem) mkdirNow(ctx context.Context, parent int, name string) error {
	cur, exists, err := d.idx.Child(ctx, parent, name)
	if err != nil {
		return err
	}
	if exists {
		if cur.IsDir {
			return nil
		}
		return os.ErrExist
	}
	id, err := d.sendNode(ctx, name, parent, true, "")
	if err != nil {
		return err
	}
	if err := d.idx.PutNode(ctx, index.Node{
		ID: id, ParentID: parent, Name: name, IsDir: true, Mtime: nowUnix(),
	}); err != nil {
		return d.reconcileNode(ctx, id)
	}
	d.notify()
	return nil
}

// renameNow 编辑一条消息完成改名/移动；已经改好的直接成功。
func (d *FileSystem) renameNow(ctx context.Context, id, parent int, name string) error {
	src, ok, err := d.idx.NodeByID(ctx, id)
	if err != nil {
		return err
	}
	if !ok {
		return d.reconcileNode(ctx, id)
	}
	if src.ParentID == parent && src.Name == name {
		return nil
	}
	body, err := nodeBody(name, parent, src.IsDir, src.Sha256)
	if err != nil {
		return err
	}
	if err := d.store.EditNode(ctx, id, body); err != nil {
		return err
	}
	if _, _, err := d.idx.MoveNode(ctx, id, parent, name); err != nil {
		return d.reconcileNode(ctx, id)
	}
	d.notify()
	return nil
}

// removeNow 删除子树；节点已不在视为已完成。
func (d *FileSystem) removeNow(ctx context.Context, id int) error {
	if _, ok, err := d.idx.NodeByID(ctx, id); err != nil {
		return err
	} else if !ok {
		return nil
	}
	ids, err := d.idx.SubtreeIDs(ctx, id)
	if err != nil {
		return err
	}
	if len(ids) > 0 {
		if err := d.store.Delete(ctx, ids...); err != nil && !telegramstore.IsMessageGone(err) {
			return err
		}
	}
	if _, err := d.idx.DeleteSubtree(ctx, id); err != nil {
		return d.reconcileNode(ctx, id)
	}
	d.notify()
	return nil
}

// uploadNow 提交临时文件；临时文件已不存在则清账收尾。
func (d *FileSystem) uploadNow(ctx context.Context, p uploadJob) error {
	tmp, err := os.Open(p.Temp)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			_ = d.idx.DeletePending(ctx, p.Path)
			return nil
		}
		return err
	}
	st, err := tmp.Stat()
	if err != nil {
		_ = tmp.Close()
		return err
	}
	if st.Size() != p.Size {
		// 临时文件被覆盖或截断：这条记账已经没有意义
		_ = tmp.Close()
		_ = d.idx.DeletePending(ctx, p.Path)
		_ = os.Remove(p.Temp)
		return nil
	}
	// 上次已经上传成功但没写成节点：只补登记，不重传
	helper := &file{d: d, name: p.Path}
	if done, ok, derr := d.idx.GetPendingDone(ctx, p.Path); derr == nil && ok && done.Sha256 == p.Sha {
		err = helper.record(ctx, p.Path, p.Sha, &telegramstore.Part{
			MessageID:     done.MessageID,
			DocumentID:    done.DocumentID,
			AccessHash:    done.AccessHash,
			FileReference: done.FileReference,
			Size:          done.Size,
		})
		if err != nil {
			_ = tmp.Close()
			return err
		}
		_ = tmp.Close()
		_ = d.idx.DeletePending(ctx, p.Path)
		_ = os.Remove(p.Temp)
		return nil
	}
	cerr := helper.commitAt(ctx, tmp, p)
	_ = tmp.Close()
	if cerr != nil {
		return cerr
	}
	_ = d.idx.DeletePending(ctx, p.Path)
	return os.Remove(p.Temp)
}
