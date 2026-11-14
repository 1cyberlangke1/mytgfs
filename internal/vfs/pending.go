package vfs

// 未提交上传的补传：把 pending 账本重新排进持久化队列。
//
// 输入：索引里的 pending 记录
// 输出：无（异步）
// 预期：
//   - 不再阻塞启动：断网时任务留在库里，联网后自动跑
//   - 只处理本程序记下的记录，不扫描频道里别的内容
//   - 临时文件丢失或大小不符的账本直接清掉，避免永远卡住

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"os"

	"mytgfs/internal/i18n"
	"mytgfs/internal/queue"
)

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
		if statErr != nil || p.Size <= 0 || st.Size() <= 0 || st.Size() != p.Size {
			// 空文件永远传不上去（Telegram 会回 FILE_PARTS_INVALID），直接清账
			_ = tmp.Close()
			_ = d.idx.DeletePending(d.ctx, p.Path)
			_ = os.Remove(p.Temp)
			continue
		}
		// 摘要按临时文件实际内容计算，作为断点键与去重依据
		h := sha256.New()
		if _, cerr := io.Copy(h, tmp); cerr != nil {
			_ = tmp.Close()
			continue
		}
		_ = tmp.Close()
		sha := hex.EncodeToString(h.Sum(nil))
		parent, base, ok, perr := d.parentOf(d.ctx, p.Path)
		if perr != nil || !ok {
			// 父目录还没重建出来：留着账本，下次启动再试
			slog.Warn(i18n.T("log.pending_resume_failed", p.Path, perr), "path", p.Path)
			continue
		}
		body, berr := nodeBody(base, parent.ID, false, sha)
		if berr != nil {
			continue
		}
		if d.q == nil {
			continue
		}
		if _, err := d.q.EnqueueOnce(d.ctx, queue.Job{
			Volume: d.volume, Kind: jobUpload, Name: p.Path,
			Payload: mustJSON(uploadJob{
				Path: p.Path, Temp: p.Temp, Size: p.Size, Sha: sha,
				Base: base, Body: body, Parent: parent.ID,
			}),
		}); err != nil {
			slog.Warn(i18n.T("log.pending_resume_failed", p.Path, err), "path", p.Path)
		}
	}
	return nil
}
