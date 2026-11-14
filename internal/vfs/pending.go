package vfs

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"os"

	"mytgfs/internal/i18n"
	"mytgfs/internal/telegramstore"
)

// 未提交上传的补传账本。
//
// 输入：索引里的 pending 记录
// 输出：补传结果
// 预期：只处理本程序记下的记录，不扫描频道里别的内容

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
