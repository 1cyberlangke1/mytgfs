package vfs

// vfs 写路径单元测试：不挂载、不连 Telegram，用临时目录和假存储。
//
// 覆盖：写打开已有文件、O_TRUNC、Truncate 扩容/截断、未知句柄。

import (
	"context"
	"fmt"
	"io"
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
	return nil
}

func newTestFS(t *testing.T, store Store) (*FS, *index.Index) {
	t.Helper()
	idx, err := index.OpenMemory()
	if err != nil {
		t.Fatalf("打开内存索引: %v", err)
	}
	t.Cleanup(func() { _ = idx.Close() })
	return New(context.Background(), idx, store, t.TempDir()), idx
}

// TestOpenWriteExistingKeepsContent 写打开已有文件，未截断时应保留原内容。
func TestOpenWriteExistingKeepsContent(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	store.data[1] = []byte("hello")
	fs, idx := newTestFS(t, store)
	if err := idx.Put(ctx, index.File{Path: "/a.txt", MessageID: 1, Size: 5, Mtime: 1}); err != nil {
		t.Fatalf("写入索引: %v", err)
	}

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
	ctx := context.Background()
	store := newFakeStore()
	store.data[1] = []byte("hello")
	fs, idx := newTestFS(t, store)
	if err := idx.Put(ctx, index.File{Path: "/a.txt", MessageID: 1, Size: 5, Mtime: 1}); err != nil {
		t.Fatalf("写入索引: %v", err)
	}

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
	fs, _ := newTestFS(t, store)

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
	fs, _ := newTestFS(t, newFakeStore())
	if rc := fs.Truncate("/missing.txt", 0, 9999); rc != -fuse.ENOENT {
		t.Fatalf("应返回 ENOENT，实际 %d", rc)
	}
}
