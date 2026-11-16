package main

// tree / du 递归逻辑的单测：用内存假目录树验证层数截断与体积汇总。

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"testing"
	"time"

	"mytgfs/internal/fm"
	"mytgfs/internal/vfs"
)

// fakeFiles 是只实现必要方法的 fm.Files 假实现。
//
// 输入：tree/info/data 提供读数据；mkdirs/removes/moves 记录写操作；
// searchHits 作为 Search 返回值；failOn 按路径注入错误
// 输出：读方法按路径返回数据；写方法把参数追加进记录切片
// 预期：未被测试使用的方法仍然 panic，暴露意外依赖
type fakeFiles struct {
	tree       map[string][]fm.Entry
	info       map[string]fm.Entry
	data       map[string][]byte
	mkdirs     []string
	removes    []string
	moves      [][2]string
	searchHits []fm.SearchHit
	failOn     map[string]error
}

// errFor 返回某路径注入的错误；未配置时返回 nil。
func (f *fakeFiles) errFor(p string) error {
	if f.failOn == nil {
		return nil
	}
	return f.failOn[p]
}

func (f *fakeFiles) List(_ context.Context, dir string) ([]fm.Entry, error) {
	return f.tree[dir], nil
}

func (f *fakeFiles) Search(context.Context, string, string, int) ([]fm.SearchHit, error) {
	return f.searchHits, nil
}

func (f *fakeFiles) Info(_ context.Context, p string) (fm.Entry, error) {
	if err := f.errFor(p); err != nil {
		return fm.Entry{}, err
	}
	e, ok := f.info[p]
	if !ok {
		return fm.Entry{}, os.ErrNotExist
	}
	return e, nil
}

func (f *fakeFiles) Mkdir(_ context.Context, p string) error {
	if err := f.errFor(p); err != nil {
		return err
	}
	f.mkdirs = append(f.mkdirs, p)
	return nil
}

func (f *fakeFiles) Upload(context.Context, string, io.Reader) error { panic("unexpected Upload") }

func (f *fakeFiles) AdoptUpload(context.Context, string, string, int64, string) error {
	panic("unexpected AdoptUpload")
}

func (f *fakeFiles) Remove(_ context.Context, p string) error {
	if err := f.errFor(p); err != nil {
		return err
	}
	f.removes = append(f.removes, p)
	return nil
}

func (f *fakeFiles) Move(_ context.Context, from, to string) error {
	if err := f.errFor(from); err != nil {
		return err
	}
	if err := f.errFor(to); err != nil {
		return err
	}
	f.moves = append(f.moves, [2]string{from, to})
	return nil
}

func (f *fakeFiles) Open(_ context.Context, p string) (vfs.File, error) {
	b, ok := f.data[p]
	if !ok {
		return nil, os.ErrNotExist
	}
	return &memFile{name: path.Base(p), r: bytes.NewReader(b), size: int64(len(b))}, nil
}

// memFile 是只读内存文件，供 getFile 测试读取远端内容。
type memFile struct {
	name string
	r    *bytes.Reader
	size int64
}

func (m *memFile) Read(p []byte) (int, error)                { return m.r.Read(p) }
func (m *memFile) Write([]byte) (int, error)                 { return 0, errors.New("read-only") }
func (m *memFile) Close() error                              { return nil }
func (m *memFile) Seek(off int64, whence int) (int64, error) { return m.r.Seek(off, whence) }

func (m *memFile) Readdir(int) ([]fs.FileInfo, error) {
	return nil, errors.New("not a directory")
}

func (m *memFile) Stat() (fs.FileInfo, error) { return memInfo{m}, nil }

// memInfo 是 memFile 的 FileInfo 视图。
type memInfo struct{ m *memFile }

func (i memInfo) Name() string       { return i.m.name }
func (i memInfo) Size() int64        { return i.m.size }
func (i memInfo) Mode() fs.FileMode  { return 0o444 }
func (i memInfo) ModTime() time.Time { return time.Unix(0, 0) }
func (i memInfo) IsDir() bool        { return false }
func (i memInfo) Sys() any           { return nil }

// testTree 构造 /root + sub + sub/deep 三层假目录。
func testTree() *fakeFiles {
	return &fakeFiles{tree: map[string][]fm.Entry{
		"/root": {
			{Name: "a.txt", Size: 10, Mod: time.Unix(0, 0)},
			{Name: "sub", Dir: true},
		},
		"/root/sub": {
			{Name: "b.bin", Size: 20},
			{Name: "deep", Dir: true},
		},
		"/root/sub/deep": {
			{Name: "c.txt", Size: 5},
		},
	}}
}

// TestWalkTree 层数不限时构造整棵树，目录体积等于子项合计。
func TestWalkTree(t *testing.T) {
	n, err := walkTree(t.Context(), testTree(), "/root", "root", 0, 0)
	if err != nil {
		t.Fatalf("walkTree: %v", err)
	}
	if !n.Dir || n.Path != "/root" || n.Size != 35 {
		t.Fatalf("根节点 = %+v", n)
	}
	if len(n.Children) != 2 || n.Children[0].Name != "a.txt" || n.Children[1].Name != "sub" {
		t.Fatalf("根子项 = %+v", n.Children)
	}
	sub := n.Children[1]
	if sub.Size != 25 || len(sub.Children) != 2 {
		t.Fatalf("sub = %+v", sub)
	}
	deep := sub.Children[1]
	if deep.Path != "/root/sub/deep" || deep.Size != 5 || len(deep.Children) != 1 {
		t.Fatalf("deep = %+v", deep)
	}
}

// TestWalkTreeDepth 层数截断后不再展开更深的子目录。
func TestWalkTreeDepth(t *testing.T) {
	n, err := walkTree(t.Context(), testTree(), "/root", "root", 0, 1)
	if err != nil {
		t.Fatalf("walkTree: %v", err)
	}
	sub := n.Children[1]
	if !sub.Dir || len(sub.Children) != 0 || sub.Size != 0 {
		t.Fatalf("--depth 1 时 sub 应被截断: %+v", sub)
	}
	if n.Size != 10 {
		t.Fatalf("截断后根体积 = %d，期望 10（只有 a.txt）", n.Size)
	}
}

// TestWalkDu 后序汇总：子目录先输出，父目录包含整棵子树。
func TestWalkDu(t *testing.T) {
	var entries []duEntry
	size, files, err := walkDu(t.Context(), testTree(), "/root", &entries)
	if err != nil {
		t.Fatalf("walkDu: %v", err)
	}
	if size != 35 || files != 3 {
		t.Fatalf("合计 = %d 字节 / %d 文件，期望 35 / 3", size, files)
	}
	want := []duEntry{
		{Path: "/root/sub/deep", Files: 1, Size: 5},
		{Path: "/root/sub", Files: 2, Size: 25},
		{Path: "/root", Files: 3, Size: 35},
	}
	if len(entries) != len(want) {
		t.Fatalf("entries = %+v", entries)
	}
	for i := range want {
		if entries[i] != want[i] {
			t.Errorf("entries[%d] = %+v，期望 %+v", i, entries[i], want[i])
		}
	}
}
