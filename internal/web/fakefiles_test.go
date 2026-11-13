package web

// 测试用的内存文件后端：只记录 Upload 落盘内容，其余方法保持空实现。

import (
	"context"
	"errors"
	"io"
	"os"

	"golang.org/x/net/webdav"
	"mytgfs/internal/fm"
)

// fakeFiles 是 fm.Files 的最小假实现。
//
// 输入：Upload 写入的内容
// 输出：uploads 记录「以 / 开头的绝对路径 → 文件内容」
// 预期：不访问磁盘，不依赖 Telegram
type fakeFiles struct {
	uploads map[string]string
	dirs    map[string]bool
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

func (f *fakeFiles) Open(ctx context.Context, p string) (webdav.File, error) {
	return nil, os.ErrNotExist
}

var _ fm.Files = (*fakeFiles)(nil)
