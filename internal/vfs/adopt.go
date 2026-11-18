package vfs

// 接管已落盘的临时文件：面板分片上传直接写临时文件，提交时不再拷贝一次。

import (
	"context"
	"errors"
	"os"

	"mytgfs/internal/i18n"
	"mytgfs/internal/index"
)

// Adopt 接管一个已经落盘的临时文件，直接入队上传。
//
// 输入：ctx；name 卷内目标路径；temp 临时文件路径；size 字节数；sha 内容摘要
// 输出：任务 id（无队列时为 0）；error
// 预期：
//   - 与「写句柄 + Close」等价，但内容不再从临时文件拷一份到本卷临时目录
//   - 只保证任务已落库就返回，不等待 Telegram 上传结束：
//     上传要跑几分钟，HTTP 提交不能一直挂着，真实进度由队列的 Report 推给面板
//   - 临时文件所有权转移给本层：成功时由执行体删除，失败时保留待重试
//   - 目标目录不存在时直接失败，不留无主消息
func (d *FileSystem) Adopt(ctx context.Context, name, temp string, size int64, sha string) (int64, error) {
	if size <= 0 {
		_ = os.Remove(temp)
		return 0, errors.New(i18n.T("err.upload_empty"))
	}
	parent, base, ok, err := d.parentOf(ctx, name)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, os.ErrNotExist
	}
	body, err := nodeBody(base, parent.ID, false, sha)
	if err != nil {
		return 0, err
	}
	// 断电前内容必须真的落盘，否则重启重放拿到的是短文件
	if err := syncFile(temp); err != nil {
		return 0, err
	}
	if err := d.idx.PutPending(ctx, index.PendingUpload{
		Path: name, Temp: temp, Size: size, Mtime: nowUnix(),
	}); err != nil {
		return 0, err
	}
	return d.enqueueAsync(ctx, jobUpload, name, uploadJob{
		Path: name, Temp: temp, Size: size, Sha: sha,
		Base: base, Body: body, Parent: parent.ID,
	})
}

// syncFile 把文件内容刷到磁盘。
//
// 输入：文件路径
// 输出：error
// 预期：只做 fsync，不改内容也不改大小
func syncFile(name string) error {
	f, err := os.OpenFile(name, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return f.Sync()
}
