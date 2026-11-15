package web

// 测试用的内存文件后端：只记录 Upload 落盘内容，其余方法保持空实现。

import (
	"context"
	"errors"
	"io"
	"os"

	"mytgfs/internal/fm"
	"mytgfs/internal/vfs"
)

// fakeFiles 是 fm.Files 的最小假实现。
//
// 输入：Upload 写入的内容
// 输出：uploads 记录「以 / 开头的绝对路径 → 文件内容」
// 预期：不访问磁盘，不依赖 Telegram
type fakeFiles struct {
	uploads map[string]string
	dirs    map[string]bool
	// adoptCalls 记录 AdoptUpload 的调用次数：提交路径不该退回再拷一份
	adoptCalls int
	// lastSha 是最后一次接管的摘要，用来断言摘要在收分片时就算好了
	lastSha string
}

func newFakeFiles() *fakeFiles {
	return &fakeFiles{uploads: map[string]string{}, dirs: map[string]bool{}}
}

func (f *fakeFiles) List(ctx context.Context, dir string) ([]fm.Entry, error) {
	return nil, nil
}

func (f *fakeFiles) Search(ctx context.Context, dir, query string, limit int) ([]fm.SearchHit, error) {
	return nil, nil
}

func (f *fakeFiles) Info(ctx context.Context, p string) (fm.Entry, error) {
	if data, ok := f.uploads[p]; ok {
		return fm.Entry{Name: p, Size: int64(len(data))}, nil
	}
	return fm.Entry{}, errors.New("not found")
}

func (f *fakeFiles) Mkdir(ctx context.Context, p string) error {
	if f.dirs[p] {
		return fm.ErrExists
	}
	f.dirs[p] = true
	return nil
}

func (f *fakeFiles) Upload(ctx context.Context, p string, r io.Reader) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	f.uploads[p] = string(data)
	return nil
}

// AdoptUpload 接管已落盘的临时文件：测试里读进内存，等价于 Upload。
//
// 输入：p 目标路径；temp 临时文件；size 字节数；sha 摘要（不校验）
// 输出：error
// 预期：记录一次接管调用，用来断言提交路径没有退回「再拷一份」
func (f *fakeFiles) AdoptUpload(ctx context.Context, p, temp string, size int64, sha string) error {
	data, err := os.ReadFile(temp)
	if err != nil {
		return err
	}
	if int64(len(data)) != size {
		return errors.New("adopt size mismatch")
	}
	f.adoptCalls++
	f.lastSha = sha
	f.uploads[p] = string(data)
	return nil
}

func (f *fakeFiles) Remove(ctx context.Context, p string) error {
	delete(f.uploads, p)
	return nil
}

func (f *fakeFiles) Move(ctx context.Context, from, to string) error {
	if data, ok := f.uploads[from]; ok {
		delete(f.uploads, from)
		f.uploads[to] = data
	}
	return nil
}

func (f *fakeFiles) Open(ctx context.Context, p string) (vfs.File, error) {
	return nil, os.ErrNotExist
}

var _ fm.Files = (*fakeFiles)(nil)
