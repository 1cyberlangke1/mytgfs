package vfs

// vfs 增删改查单元测试：不挂载、不连 Telegram，用临时目录和假存储。

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/winfsp/cgofuse/fuse"

	"mytgfs/internal/index"
	"mytgfs/internal/telegramstore"
)

// fakeStore 在内存里模拟 Telegram 存储。
type fakeStore struct {
	data      map[int][]byte // message id -> 内容
	nextID    int
	uploads   []string // 每次 Upload 的完整内容
	readCalls int
	deletes   []int
	renames   []string // 每次 Rename 的 caption
}

func newFakeStore() *fakeStore {
	return &fakeStore{data: map[int][]byte{}}
}

func (s *fakeStore) Upload(_ context.Context, r io.Reader, size int64, name, mime, caption string) (*telegramstore.Part, error) {
	b, err := io.ReadAll(io.LimitReader(r, size))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) != size {
		return nil, fmt.Errorf("上传长度 %d 与声明 %d 不一致", len(b), size)
	}
	s.nextID++
	s.data[s.nextID] = b
	s.uploads = append(s.uploads, string(b))
	return &telegramstore.Part{MessageID: s.nextID, Size: size}, nil
}

func (s *fakeStore) ReadRange(_ context.Context, p *telegramstore.Part, offset int64, length int) ([]byte, error) {
	s.readCalls++
	b := s.data[p.MessageID]
	if offset >= int64(len(b)) {
		return nil, nil
	}
	end := offset + int64(length)
	if end > int64(len(b)) {
		end = int64(len(b))
	}
	return b[offset:end], nil
}

func (s *fakeStore) Delete(_ context.Context, ids ...int) error {
	s.deletes = append(s.deletes, ids...)
	return nil
}

func (s *fakeStore) Rename(_ context.Context, messageID int, caption string) error {
	s.renames = append(s.renames, caption)
	return nil
}

// newTestFS 创建带通知记录的测试文件系统。
func newTestFS(t *testing.T, store Store) (*FS, *index.Index, *[]uint32) {
	t.Helper()
	idx, err := index.OpenMemory()
	if err != nil {
		t.Fatalf("打开内存索引: %v", err)
	}
	t.Cleanup(func() { _ = idx.Close() })
	fs := New(context.Background(), idx, store, t.TempDir())
	var events []uint32
	fs.SetNotify(func(p string, action uint32) { events = append(events, action) })
	return fs, idx, &events
}

func putFile(t *testing.T, idx *index.Index, f index.File) {
	t.Helper()
	if err := idx.Put(context.Background(), f); err != nil {
		t.Fatalf("写入索引: %v", err)
	}
}

// TestOpenWriteExistingKeepsContent 写打开已有文件，未截断时应保留原内容。
func TestOpenWriteExistingKeepsContent(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	store.data[1] = []byte("hello")
	fs, idx, _ := newTestFS(t, store)
	putFile(t, idx, index.File{Path: "/a.txt", MessageID: 1, Size: 5, Mtime: 1})

	rc, fh := fs.Open("/a.txt", fuse.O_WRONLY)
	if rc != 0 {
		t.Fatalf("Open 失败: %d", rc)
	}
	if n := fs.Write("/a.txt", []byte("HELLO"), 0, fh); n != 5 {
		t.Fatalf("Write 返回 %d", n)
	}
	if rc := fs.Release("/a.txt", fh); rc != 0 {
		t.Fatalf("Release 失败: %d", rc)
	}
	if len(store.uploads) != 1 || store.uploads[0] != "HELLO" {
		t.Fatalf("上传内容应为 HELLO，实际 %q", store.uploads)
	}
	if len(store.deletes) != 1 || store.deletes[0] != 1 {
		t.Fatalf("覆盖写应删除旧消息 1，实际 %v", store.deletes)
	}
	got, ok, err := idx.Get(ctx, "/a.txt")
	if err != nil || !ok {
		t.Fatalf("读索引: ok=%v err=%v", ok, err)
	}
	if got.MessageID != store.nextID || got.Size != 5 {
		t.Fatalf("索引未更新为最新消息: %+v", got)
	}
}

// TestOpenWriteExistingWithTruncate O_TRUNC 打开时不应复制原内容。
func TestOpenWriteExistingWithTruncate(t *testing.T) {
	store := newFakeStore()
	store.data[1] = []byte("hello")
	fs, idx, _ := newTestFS(t, store)
	putFile(t, idx, index.File{Path: "/a.txt", MessageID: 1, Size: 5, Mtime: 1})

	rc, fh := fs.Open("/a.txt", fuse.O_WRONLY|fuse.O_TRUNC)
	if rc != 0 {
		t.Fatalf("Open 失败: %d", rc)
	}
	if store.readCalls != 0 {
		t.Fatalf("O_TRUNC 不应读取原内容，实际读取 %d 次", store.readCalls)
	}
	if n := fs.Write("/a.txt", []byte("new"), 0, fh); n != 3 {
		t.Fatalf("Write 返回 %d", n)
	}
	if rc := fs.Release("/a.txt", fh); rc != 0 {
		t.Fatalf("Release 失败: %d", rc)
	}
	if len(store.uploads) != 1 || store.uploads[0] != "new" {
		t.Fatalf("上传内容应为 new，实际 %q", store.uploads)
	}
}

// TestTruncatePending 对新建文件先写后截断，最终上传截断后的内容。
func TestTruncatePending(t *testing.T) {
	store := newFakeStore()
	fs, _, _ := newTestFS(t, store)

	rc, fh := fs.Open("/new.txt", fuse.O_WRONLY|fuse.O_CREAT)
	if rc != 0 {
		t.Fatalf("Open 失败: %d", rc)
	}
	if n := fs.Write("/new.txt", []byte("abcdef"), 0, fh); n != 6 {
		t.Fatalf("Write 返回 %d", n)
	}
	if rc := fs.Truncate("/new.txt", 3, fh); rc != 0 {
		t.Fatalf("Truncate 失败: %d", rc)
	}
	if rc := fs.Release("/new.txt", fh); rc != 0 {
		t.Fatalf("Release 失败: %d", rc)
	}
	if len(store.uploads) != 1 || store.uploads[0] != "abc" {
		t.Fatalf("上传内容应为 abc，实际 %q", store.uploads)
	}
}

// TestTruncateUnknownHandle 未知句柄返回 ENOENT。
func TestTruncateUnknownHandle(t *testing.T) {
	fs, _, _ := newTestFS(t, newFakeStore())
	if rc := fs.Truncate("/missing.txt", 0, 9999); rc != -fuse.ENOENT {
		t.Fatalf("应返回 ENOENT，实际 %d", rc)
	}
}

// TestCreateReadDelete 新建、读回、删除一条完整回合。
func TestCreateReadDelete(t *testing.T) {
	store := newFakeStore()
	fs, idx, events := newTestFS(t, store)

	rc, fh := fs.Open("/f.txt", fuse.O_WRONLY|fuse.O_CREAT)
	if rc != 0 {
		t.Fatalf("Open 失败: %d", rc)
	}
	if n := fs.Write("/f.txt", []byte("abc"), 0, fh); n != 3 {
		t.Fatalf("Write 返回 %d", n)
	}
	if rc := fs.Release("/f.txt", fh); rc != 0 {
		t.Fatalf("Release 失败: %d", rc)
	}

	buf := make([]byte, 8)
	if n := fs.Read("/f.txt", buf, 0, 0); n != 3 || string(buf[:n]) != "abc" {
		t.Fatalf("读回失败: n=%d content=%q", n, buf[:n])
	}
	if rc := fs.Unlink("/f.txt"); rc != 0 {
		t.Fatalf("Unlink 失败: %d", rc)
	}
	if _, ok, _ := idx.Get(context.Background(), "/f.txt"); ok {
		t.Fatal("删除后索引仍有记录")
	}
	if len(store.deletes) != 1 || store.deletes[0] != store.nextID {
		t.Fatalf("删除应调用消息 %d，实际 %v", store.nextID, store.deletes)
	}
	if len(*events) == 0 || (*events)[len(*events)-1] != fuse.NOTIFY_UNLINK {
		t.Fatalf("缺少 UNLINK 通知: %v", *events)
	}
}

// TestMkdirRmdir 空目录可持久化，非空目录不能删。
func TestMkdirRmdir(t *testing.T) {
	fs, idx, events := newTestFS(t, newFakeStore())
	ctx := context.Background()

	if rc := fs.Mkdir("/docs", 0o777); rc != 0 {
		t.Fatalf("Mkdir 失败: %d", rc)
	}
	if rc := fs.Mkdir("/docs", 0o777); rc != -fuse.EEXIST {
		t.Fatalf("重复 Mkdir 应返回 EEXIST，实际 %d", rc)
	}
	entries, err := idx.List(ctx, "/")
	if err != nil {
		t.Fatalf("列目录: %v", err)
	}
	found := false
	for _, e := range entries {
		if e.Name == "docs" && e.IsDir {
			found = true
		}
	}
	if !found {
		t.Fatal("空目录没有出现在列表里")
	}
	if !fs.isDir("/docs") {
		t.Fatal("isDir 判断失败")
	}

	putFile(t, idx, index.File{Path: "/docs/f.txt", MessageID: 1, Size: 1})
	if rc := fs.Rmdir("/docs"); rc != -fuse.ENOTEMPTY {
		t.Fatalf("非空目录应返回 ENOTEMPTY，实际 %d", rc)
	}
	if err := idx.Delete(ctx, "/docs/f.txt"); err != nil {
		t.Fatalf("清理测试文件: %v", err)
	}
	if rc := fs.Rmdir("/docs"); rc != 0 {
		t.Fatalf("Rmdir 失败: %d", rc)
	}
	if fs.isDir("/docs") {
		t.Fatal("删除后目录仍然存在")
	}

	hasMkdir, hasRmdir := false, false
	for _, e := range *events {
		if e == fuse.NOTIFY_MKDIR {
			hasMkdir = true
		}
		if e == fuse.NOTIFY_RMDIR {
			hasRmdir = true
		}
	}
	if !hasMkdir || !hasRmdir {
		t.Fatalf("缺少目录通知: %v", *events)
	}
}

// TestRenameFile 文件改名：更新 caption 与索引。
func TestRenameFile(t *testing.T) {
	store := newFakeStore()
	fs, idx, _ := newTestFS(t, store)
	putFile(t, idx, index.File{Path: "/a.txt", MessageID: 1, Size: 5})

	if rc := fs.Rename("/a.txt", "/b.txt"); rc != 0 {
		t.Fatalf("Rename 失败: %d", rc)
	}
	if _, ok, _ := idx.Get(context.Background(), "/a.txt"); ok {
		t.Fatal("旧路径仍在索引")
	}
	got, ok, _ := idx.Get(context.Background(), "/b.txt")
	if !ok || got.MessageID != 1 || got.Path != "/b.txt" {
		t.Fatalf("新路径索引错误: %+v ok=%v", got, ok)
	}
	if len(store.renames) != 1 || !strings.Contains(store.renames[0], "/b.txt") {
		t.Fatalf("caption 未更新: %v", store.renames)
	}
}

// TestRenameReplacesTarget 覆盖已有目标时删除目标旧消息。
func TestRenameReplacesTarget(t *testing.T) {
	store := newFakeStore()
	fs, idx, _ := newTestFS(t, store)
	putFile(t, idx, index.File{Path: "/a.txt", MessageID: 1, Size: 5})
	putFile(t, idx, index.File{Path: "/b.txt", MessageID: 2, Size: 6})

	if rc := fs.Rename("/a.txt", "/b.txt"); rc != 0 {
		t.Fatalf("Rename 失败: %d", rc)
	}
	if len(store.deletes) != 1 || store.deletes[0] != 2 {
		t.Fatalf("应删除目标旧消息 2，实际 %v", store.deletes)
	}
	got, ok, _ := idx.Get(context.Background(), "/b.txt")
	if !ok || got.MessageID != 1 {
		t.Fatalf("目标索引错误: %+v ok=%v", got, ok)
	}
}

// TestRenameDir 目录改名：整棵子树与目录记录一起移动。
func TestRenameDir(t *testing.T) {
	store := newFakeStore()
	fs, idx, _ := newTestFS(t, store)
	ctx := context.Background()
	if err := idx.PutDir(ctx, "/d"); err != nil {
		t.Fatalf("PutDir: %v", err)
	}
	putFile(t, idx, index.File{Path: "/d/x.txt", MessageID: 1, Size: 1})
	putFile(t, idx, index.File{Path: "/d/y.txt", MessageID: 2, Size: 2})

	if rc := fs.Rename("/d", "/e"); rc != 0 {
		t.Fatalf("目录 Rename 失败: %d", rc)
	}
	if fs.isDir("/d") {
		t.Fatal("旧目录仍在")
	}
	if !fs.isDir("/e") {
		t.Fatal("新目录不存在")
	}
	for _, p := range []string{"/e/x.txt", "/e/y.txt"} {
		if _, ok, _ := idx.Get(ctx, p); !ok {
			t.Fatalf("子文件未移动: %s", p)
		}
	}
	if len(store.renames) != 2 {
		t.Fatalf("应改两条 caption，实际 %v", store.renames)
	}
}
