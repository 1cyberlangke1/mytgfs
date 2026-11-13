package fm

// 文件管理门面单测：用内存文件系统验证列出、上传、递归删除、移动与路径校验。
//
// 内存实现刻意模仿 dav 的提交语义——Close 才落盘、Abort 丢弃——这样
// 「写一半失败不能留脏文件」这条也能被测到。

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/webdav"
)

// ---- 内存 webdav.FileSystem ----

type memFS struct {
	files map[string][]byte
	dirs  map[string]bool
}

func newMemFS() *memFS {
	return &memFS{files: map[string][]byte{}, dirs: map[string]bool{"/": true}}
}

func (m *memFS) Mkdir(_ context.Context, name string, _ os.FileMode) error {
	if m.dirs[name] {
		return os.ErrExist
	}
	if _, ok := m.files[name]; ok {
		return os.ErrExist
	}
	m.dirs[name] = true
	return nil
}

func (m *memFS) OpenFile(_ context.Context, name string, flag int, _ os.FileMode) (webdav.File, error) {
	if flag&(os.O_WRONLY|os.O_RDWR|os.O_CREATE) != 0 {
		return &memFile{fs: m, name: name, write: true}, nil
	}
	if m.dirs[name] {
		return &memFile{fs: m, name: name, dir: true, entries: m.children(name)}, nil
	}
	data, ok := m.files[name]
	if !ok {
		return nil, os.ErrNotExist
	}
	return &memFile{fs: m, name: name, data: data}, nil
}

// RemoveAll 删文件，或删目录及其全部子孙（模仿 WebDAV 的递归语义）。
func (m *memFS) RemoveAll(_ context.Context, name string) error {
	if name == "/" {
		return nil
	}
	if _, ok := m.files[name]; ok {
		delete(m.files, name)
		return nil
	}
	if !m.dirs[name] {
		return os.ErrNotExist
	}
	delete(m.dirs, name)
	for p := range m.files {
		if strings.HasPrefix(p, name+"/") {
			delete(m.files, p)
		}
	}
	for p := range m.dirs {
		if strings.HasPrefix(p, name+"/") {
			delete(m.dirs, p)
		}
	}
	return nil
}

func (m *memFS) Rename(_ context.Context, oldName, newName string) error {
	if data, ok := m.files[oldName]; ok {
		delete(m.files, oldName)
		m.files[newName] = data
		return nil
	}
	if !m.dirs[oldName] {
		return os.ErrNotExist
	}
	// 目录：整体搬走
	for p, v := range m.files {
		if strings.HasPrefix(p, oldName+"/") {
			delete(m.files, p)
			m.files[newName+strings.TrimPrefix(p, oldName)] = v
		}
	}
	for p := range m.dirs {
		if strings.HasPrefix(p, oldName+"/") {
			delete(m.dirs, p)
			m.dirs[newName+strings.TrimPrefix(p, oldName)] = true
		}
	}
	delete(m.dirs, oldName)
	m.dirs[newName] = true
	return nil
}

func (m *memFS) Stat(_ context.Context, name string) (fs.FileInfo, error) {
	if name == "/" || m.dirs[name] {
		return fileInfo{name: path.Base(name), dir: true, mod: time.Unix(0, 0)}, nil
	}
	data, ok := m.files[name]
	if !ok {
		return nil, os.ErrNotExist
	}
	return fileInfo{name: path.Base(name), size: int64(len(data)), mod: time.Unix(0, 0)}, nil
}

// children 返回直接子项。
func (m *memFS) children(dir string) []fs.FileInfo {
	prefix := "/"
	if dir != "/" {
		prefix = dir + "/"
	}
	seen := map[string]bool{}
	var out []fs.FileInfo
	for p, data := range m.files {
		if !strings.HasPrefix(p, prefix) {
			continue
		}
		rest := strings.TrimPrefix(p, prefix)
		if i := strings.IndexByte(rest, '/'); i >= 0 {
			name := rest[:i]
			if !seen[name] {
				seen[name] = true
				out = append(out, fileInfo{name: name, dir: true})
			}
			continue
		}
		out = append(out, fileInfo{name: rest, size: int64(len(data))})
	}
	for p := range m.dirs {
		if p == dir || !strings.HasPrefix(p, prefix) {
			continue
		}
		rest := strings.TrimPrefix(p, prefix)
		if i := strings.IndexByte(rest, '/'); i >= 0 {
			rest = rest[:i]
		}
		if !seen[rest] {
			seen[rest] = true
			out = append(out, fileInfo{name: rest, dir: true})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

type memFile struct {
	fs      *memFS
	name    string
	data    []byte
	off     int
	dir     bool
	entries []fs.FileInfo
	write   bool
	buf     bytes.Buffer
	aborted bool
}

func (f *memFile) Read(p []byte) (int, error) {
	if f.off >= len(f.data) {
		return 0, io.EOF
	}
	n := copy(p, f.data[f.off:])
	f.off += n
	return n, nil
}

func (f *memFile) Write(p []byte) (int, error) { return f.buf.Write(p) }

func (f *memFile) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
		f.off = int(offset)
	case io.SeekCurrent:
		f.off += int(offset)
	case io.SeekEnd:
		f.off = len(f.data) + int(offset)
	default:
		return 0, errors.New("bad whence")
	}
	return int64(f.off), nil
}

func (f *memFile) Readdir(int) ([]fs.FileInfo, error) { return f.entries, nil }

func (f *memFile) Stat() (fs.FileInfo, error) {
	if f.dir {
		return fileInfo{name: path.Base(f.name), dir: true}, nil
	}
	return fileInfo{name: path.Base(f.name), size: int64(len(f.data))}, nil
}

func (f *memFile) Close() error {
	if f.write && !f.aborted {
		f.fs.files[f.name] = append([]byte(nil), f.buf.Bytes()...)
		delete(f.fs.dirs, f.name)
	}
	return nil
}

// Abort 丢弃内容，不落盘。
func (f *memFile) Abort() error {
	f.aborted = true
	return nil
}

type fileInfo struct {
	name string
	size int64
	dir  bool
	mod  time.Time
}

func (i fileInfo) Name() string { return i.name }
func (i fileInfo) Size() int64  { return i.size }
func (i fileInfo) Mode() fs.FileMode {
	if i.dir {
		return fs.ModeDir | 0o755
	}
	return 0o644
}
func (i fileInfo) ModTime() time.Time { return i.mod }
func (i fileInfo) IsDir() bool        { return i.dir }
func (i fileInfo) Sys() any           { return nil }

// errReader 先吐一点数据再报错，用来模拟上传中断。
type errReader struct {
	left int
}

func (e *errReader) Read(p []byte) (int, error) {
	if e.left <= 0 {
		return 0, errors.New("boom")
	}
	e.left--
	return len(p), nil
}

// ---- 测试 ----

func TestListDirsFirst(t *testing.T) {
	m := newMemFS()
	m.files["/b.txt"] = []byte("bb")
	m.files["/a.txt"] = []byte("a")
	m.dirs["/zed"] = true
	got, err := New(m).List(context.Background(), "/")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("条目数 = %d: %+v", len(got), got)
	}
	if !got[0].Dir || got[0].Name != "zed" {
		t.Errorf("目录应排最前，实际第一个 = %+v", got[0])
	}
	if got[1].Name != "a.txt" || got[2].Name != "b.txt" {
		t.Errorf("文件应按名字排序: %+v", got)
	}
}

func TestUploadCommits(t *testing.T) {
	m := newMemFS()
	if err := New(m).Upload(context.Background(), "/dir/x.bin", strings.NewReader("hello")); err != nil {
		t.Fatal(err)
	}
	if string(m.files["/dir/x.bin"]) != "hello" {
		t.Errorf("内容 = %q", m.files["/dir/x.bin"])
	}
}

// TestUploadFailureDiscards 上传中断不能留下半份内容，也不能覆盖已有文件。
func TestUploadFailureDiscards(t *testing.T) {
	m := newMemFS()
	m.files["/keep.bin"] = []byte("original")
	err := New(m).Upload(context.Background(), "/keep.bin", &errReader{left: 1})
	if err == nil {
		t.Fatal("应返回错误")
	}
	if string(m.files["/keep.bin"]) != "original" {
		t.Errorf("旧内容被破坏: %q", m.files["/keep.bin"])
	}
}

func TestMkdirRejectsExisting(t *testing.T) {
	m := newMemFS()
	mgr := New(m)
	if err := mgr.Mkdir(context.Background(), "/d"); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Mkdir(context.Background(), "/d"); !errors.Is(err, ErrExists) {
		t.Errorf("重复建目录应返回 ErrExists，实际 %v", err)
	}
	if err := mgr.Mkdir(context.Background(), "/"); !errors.Is(err, ErrBadPath) {
		t.Errorf("卷根应返回 ErrBadPath，实际 %v", err)
	}
}

// TestRemoveRecursive 删目录要把子孙一起删掉。
func TestRemoveRecursive(t *testing.T) {
	m := newMemFS()
	m.files["/d/a.txt"] = []byte("a")
	m.files["/d/sub/b.txt"] = []byte("b")
	m.dirs["/d"] = true
	m.dirs["/d/sub"] = true
	if err := New(m).Remove(context.Background(), "/d"); err != nil {
		t.Fatal(err)
	}
	if len(m.files) != 0 || len(m.dirs) != 1 { // 只剩根
		t.Errorf("残留 files=%v dirs=%v", m.files, m.dirs)
	}
}

func TestRemoveRootRejected(t *testing.T) {
	if err := New(newMemFS()).Remove(context.Background(), "/"); !errors.Is(err, ErrBadPath) {
		t.Errorf("卷根应拒绝，实际 %v", err)
	}
}

func TestMoveFile(t *testing.T) {
	m := newMemFS()
	m.files["/a.txt"] = []byte("a")
	mgr := New(m)
	if err := mgr.Move(context.Background(), "/a.txt", "/sub/a.txt"); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.files["/a.txt"]; ok {
		t.Error("源文件应被移走")
	}
	if string(m.files["/sub/a.txt"]) != "a" {
		t.Errorf("目标内容 = %q", m.files["/sub/a.txt"])
	}
}

// TestMoveDir 目录移动要把整棵树搬过去。
func TestMoveDir(t *testing.T) {
	m := newMemFS()
	m.files["/src/a.txt"] = []byte("a")
	m.files["/src/sub/b.txt"] = []byte("b")
	m.dirs["/src"] = true
	m.dirs["/src/sub"] = true
	if err := New(m).Move(context.Background(), "/src", "/dst"); err != nil {
		t.Fatal(err)
	}
	if string(m.files["/dst/a.txt"]) != "a" || string(m.files["/dst/sub/b.txt"]) != "b" {
		t.Errorf("树未整体迁移: %v", m.files)
	}
	if !m.dirs["/dst"] || !m.dirs["/dst/sub"] {
		t.Errorf("目录记录缺失: %v", m.dirs)
	}
	if _, ok := m.dirs["/src"]; ok {
		t.Error("源目录记录应被删除")
	}
}

func TestMoveRejectsTargetExists(t *testing.T) {
	m := newMemFS()
	m.files["/a.txt"] = []byte("a")
	m.files["/b.txt"] = []byte("b")
	if err := New(m).Move(context.Background(), "/a.txt", "/b.txt"); !errors.Is(err, ErrExists) {
		t.Errorf("目标已存在应报 ErrExists，实际 %v", err)
	}
}

// TestMoveDirIntoItself 不允许把目录移进自己的子目录。
func TestMoveDirIntoItself(t *testing.T) {
	m := newMemFS()
	m.dirs["/d"] = true
	m.dirs["/d/sub"] = true
	if err := New(m).Move(context.Background(), "/d", "/d/sub/d"); !errors.Is(err, ErrBadPath) {
		t.Errorf("应拒绝移入自身子树，实际 %v", err)
	}
}

// TestCleanRejectsDotDot 任何 .. 段都要拒绝。
func TestCleanRejectsDotDot(t *testing.T) {
	cases := []string{"/../etc/passwd", "a/../../b", `/x/../y`}
	for _, c := range cases {
		if _, err := clean(c); !errors.Is(err, ErrBadPath) {
			t.Errorf("clean(%q) 应拒绝，实际 %v", c, err)
		}
	}
	if got, err := clean("a/b/"); err != nil || got != "/a/b" {
		t.Errorf("clean(a/b/) = %q, %v", got, err)
	}
}

// TestOpenReads 下载用 Open 能读到正确内容。
func TestOpenReads(t *testing.T) {
	m := newMemFS()
	m.files["/x.bin"] = []byte("payload")
	f, err := New(m).Open(context.Background(), "/x.bin")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	b, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "payload" {
		t.Errorf("内容 = %q", b)
	}
}

// TestSearchFallsBackToList 底层没有索引搜索时只过滤当前目录。
func TestSearchFallsBackToList(t *testing.T) {
	mfs := newMemFS()
	for name, body := range map[string]string{"/a.txt": "a", "/b.txt": "b"} {
		mfs.files[name] = []byte(body)
	}
	m := New(mfs)

	hits, err := m.Search(context.Background(), "/", "A.", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Path != "/a.txt" || hits[0].Dir {
		t.Fatalf("退化搜索应只命中当前目录的文件: %+v", hits)
	}

	if hits, err := m.Search(context.Background(), "/", "  ", 0); err != nil || len(hits) != 0 {
		t.Fatalf("空查询应返回空: %+v %v", hits, err)
	}
	if _, err := m.Search(context.Background(), "/..", "a", 0); err == nil {
		t.Fatal("越界路径应被拒绝")
	}
}
