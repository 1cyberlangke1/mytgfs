package vfs

// vfs 增删改查单元测试：不挂载、不连 Telegram，用临时目录和假存储。

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/winfsp/cgofuse/fuse"

	"github.com/gotd/td/tgerr"

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
	renameIDs []int    // 每次 Rename 的 message id

	// uploadStarted 在 Upload 开始时关闭；uploadGate 关闭后才继续。
	// 用于测试「提交与改名并发」的时序。
	uploadStarted chan struct{}
	uploadGate    chan struct{}

	// expired 里的消息号下一次读取会返回 FILE_REFERENCE_EXPIRED；
	// refreshErr 让 Refresh 直接失败，用于覆盖刷新失败的路径。
	expired      map[int]bool
	refreshErr   error
	refreshCalls int
}

func newFakeStore() *fakeStore {
	return &fakeStore{data: map[int][]byte{}}
}

// Refresh 模拟按消息 ID 重新拉取 document 元数据。
func (s *fakeStore) Refresh(_ context.Context, messageID int) (*telegramstore.Part, error) {
	s.refreshCalls++
	if s.refreshErr != nil {
		return nil, s.refreshErr
	}
	return &telegramstore.Part{
		MessageID:     messageID,
		DocumentID:    1,
		AccessHash:    2,
		FileReference: []byte("new-ref"),
		Size:          int64(len(s.data[messageID])),
	}, nil
}

func (s *fakeStore) Upload(_ context.Context, r io.Reader, size int64, name, mime, caption string) (*telegramstore.Part, error) {
	if s.uploadStarted != nil {
		close(s.uploadStarted)
	}
	if s.uploadGate != nil {
		<-s.uploadGate
	}
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
	// 模拟 Telegram 的 file_reference 过期：第一次读报错，刷新后即可读
	if s.expired[p.MessageID] {
		delete(s.expired, p.MessageID)
		return nil, tgerr.New(400, "FILE_REFERENCE_EXPIRED")
	}
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
	s.renameIDs = append(s.renameIDs, messageID)
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

// TestVisibleDuringCommit 提交上传期间，文件名和内容仍然可读。
//
// 输入：Create+Write 后 Release 已进入 Upload（被闸门挡住），此时按路径 Getattr + Read
// 输出：Getattr 返回的大小、Read 返回的内容
// 预期：大小等于写入字节数、内容完整，而不是 ENOENT 或 0
func TestVisibleDuringCommit(t *testing.T) {
	store := newFakeStore()
	store.uploadStarted = make(chan struct{})
	store.uploadGate = make(chan struct{})
	fs, _, _ := newTestFS(t, store)

	rc, fh := fs.Create("/new.txt", fuse.O_WRONLY, 0o666)
	if rc != 0 {
		t.Fatalf("Create 失败: %d", rc)
	}
	if n := fs.Write("/new.txt", []byte("hello world"), 0, fh); n != 11 {
		t.Fatalf("Write 返回 %d", n)
	}

	released := make(chan int, 1)
	go func() { released <- fs.Release("/new.txt", fh) }()
	<-store.uploadStarted // 提交已进入 Upload，尚未写索引

	var st fuse.Stat_t
	if rc := fs.Getattr("/new.txt", &st, 0); rc != 0 {
		t.Fatalf("提交期间 Getattr 失败: %d", rc)
	}
	if st.Size != 11 {
		t.Fatalf("提交期间大小应为 11，实际 %d", st.Size)
	}
	buf := make([]byte, 32)
	n := fs.Read("/new.txt", buf, 0, 0)
	if n != 11 {
		t.Fatalf("提交期间读取应返回 11 字节，实际 %d", n)
	}
	if got := string(buf[:n]); got != "hello world" {
		t.Fatalf("提交期间内容错误: %q", got)
	}

	close(store.uploadGate)
	if rc := <-released; rc != 0 {
		t.Fatalf("Release 失败: %d", rc)
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

// TestRenameWaitsForCommit 关闭句柄的提交与改名并发时，改名必须等到新消息号写入索引。
//
// 输入：写句柄先 Release（Upload 被闸门挡住），同时发起 Rename
// 输出：Rename 使用的消息号
// 预期：Rename 拿到本次上传的新消息号，而不是被删掉的旧消息号
func TestRenameWaitsForCommit(t *testing.T) {
	store := newFakeStore()
	store.data[1] = []byte("old")
	store.nextID = 1 // 预置的旧消息占用 1，本次上传应为 2
	store.uploadStarted = make(chan struct{})
	store.uploadGate = make(chan struct{})
	fs, idx, _ := newTestFS(t, store)
	putFile(t, idx, index.File{Path: "/a.txt", MessageID: 1, Size: 3})

	rc, fh := fs.Open("/a.txt", fuse.O_WRONLY|fuse.O_TRUNC)
	if rc != 0 {
		t.Fatalf("Open 失败: %d", rc)
	}
	if n := fs.Write("/a.txt", []byte("new"), 0, fh); n != 3 {
		t.Fatalf("Write 返回 %d", n)
	}

	released := make(chan int, 1)
	go func() { released <- fs.Release("/a.txt", fh) }()
	<-store.uploadStarted // 提交已经进入 Upload，此时索引里还是旧的 1

	renamed := make(chan int, 1)
	go func() { renamed <- fs.Rename("/a.txt", "/b.txt") }()

	close(store.uploadGate) // 放行上传，Release 先完成，Rename 才能继续
	if rc := <-released; rc != 0 {
		t.Fatalf("Release 失败: %d", rc)
	}
	if rc := <-renamed; rc != 0 {
		t.Fatalf("Rename 失败: %d", rc)
	}
	if len(store.renameIDs) != 1 || store.renameIDs[0] != 2 {
		t.Fatalf("改名应使用新消息号 2，实际 %v", store.renameIDs)
	}
	got, ok, err := idx.Get(context.Background(), "/b.txt")
	if err != nil || !ok || got.MessageID != 2 {
		t.Fatalf("改名后索引错误: %+v ok=%v err=%v", got, ok, err)
	}
}

// TestReadRefreshesFileReference file_reference 过期时按消息号刷新并把新引用写回索引。
//
// 输入：索引里一条旧引用的文件，假存储第一次读取返回 FILE_REFERENCE_EXPIRED
// 输出：读取成功拿到原始内容
// 预期：只刷新一次，索引里的 file_reference 更新为刷新结果
func TestReadRefreshesFileReference(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	store.data[5] = []byte("hello")
	store.expired = map[int]bool{5: true}
	fs, idx, _ := newTestFS(t, store)
	putFile(t, idx, index.File{
		Path: "/r.txt", MessageID: 5, DocumentID: 9, AccessHash: 8,
		FileReference: []byte("old-ref"), Size: 5,
	})

	buf := make([]byte, 8)
	n := fs.Read("/r.txt", buf, 0, 0)
	if n != 5 || string(buf[:n]) != "hello" {
		t.Fatalf("刷新后读取失败: n=%d content=%q", n, buf[:n])
	}
	if store.refreshCalls != 1 {
		t.Fatalf("应刷新 1 次，实际 %d", store.refreshCalls)
	}
	got, ok, err := idx.Get(ctx, "/r.txt")
	if err != nil || !ok {
		t.Fatalf("索引读取失败: ok=%v err=%v", ok, err)
	}
	if string(got.FileReference) != "new-ref" {
		t.Fatalf("新引用未写回索引: %q", got.FileReference)
	}
}

// TestReadRefreshFailure 刷新失败时返回 EIO，不把错误当成功。
func TestReadRefreshFailure(t *testing.T) {
	store := newFakeStore()
	store.data[5] = []byte("hello")
	store.expired = map[int]bool{5: true}
	store.refreshErr = errors.New("getMessages failed")
	fs, idx, _ := newTestFS(t, store)
	putFile(t, idx, index.File{Path: "/r.txt", MessageID: 5, Size: 5})

	buf := make([]byte, 8)
	if n := fs.Read("/r.txt", buf, 0, 0); n != -fuse.EIO {
		t.Fatalf("应返回 EIO，实际 %d", n)
	}
}
