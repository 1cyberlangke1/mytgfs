package vfs

// 测试替身：内存里的远端节点消息与文件元数据。

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"testing"

	"mytgfs/internal/dirent"
	"mytgfs/internal/index"
	"mytgfs/internal/push"
	"mytgfs/internal/telegramstore"
)

// nodeMsg 是假频道里的一条消息。
type nodeMsg struct {
	body string
	part *telegramstore.Part
}

// fakeStore 记录调用并维护内存里的节点消息表。
type fakeStore struct {
	msgs map[int]nodeMsg
	// edits 记录每次 EditNode 的 (消息号, 新正文)
	edits      map[int]string
	nextID     int
	uploadKeys []string
	// uploadCaptions 记录上传时写进文件消息的 caption
	uploadCaptions []string
	uploadSizes    []int64
	deleted        []int
	copiedSrc      []int64
	readErr        error
	uploadErr      error
	copyErr        error
	refreshCall    int
	refreshedRef   []byte
	refreshErr     error
	afterRefresh   func()
	copyPart       *telegramstore.Part
	readData       []byte
	editErr        error
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		msgs:     map[int]nodeMsg{},
		edits:    map[int]string{},
		nextID:   100,
		readData: []byte("x"),
	}
}

// SendNode 发一条节点消息，正文按 dirent 解析后存进内存表。
func (f *fakeStore) SendNode(_ context.Context, body string) (int, error) {
	f.nextID++
	id := f.nextID
	if n, ok := dirent.Parse(body); ok {
		f.msgs[id] = nodeMsg{body: body}
		if !n.Dir {
			f.msgs[id] = nodeMsg{body: body, part: &telegramstore.Part{MessageID: id, DocumentID: int64(1000 + id), Size: 1}}
		}
		return id, nil
	}
	f.msgs[id] = nodeMsg{body: body}
	return id, nil
}

// EditNode 改写节点正文。
func (f *fakeStore) EditNode(_ context.Context, id int, body string) error {
	if f.editErr != nil {
		return f.editErr
	}
	f.edits[id] = body
	if m, ok := f.msgs[id]; ok {
		m.body = body
		f.msgs[id] = m
	}
	return nil
}

// ReadNode 按消息号读回正文；消息已删时返回错误，模拟远端权威。
func (f *fakeStore) ReadNode(_ context.Context, id int) (string, error) {
	m, ok := f.msgs[id]
	if !ok {
		return "", errors.New("no such message")
	}
	return m.body, nil
}

func (f *fakeStore) Copy(_ context.Context, src *telegramstore.Part, _, _, caption string) (*telegramstore.Part, error) {
	if f.copyErr != nil {
		return nil, f.copyErr
	}
	f.copiedSrc = append(f.copiedSrc, src.DocumentID)
	if f.copyPart != nil {
		return f.copyPart, nil
	}
	f.nextID++
	id := f.nextID
	f.msgs[id] = nodeMsg{body: caption, part: &telegramstore.Part{
		MessageID: id, DocumentID: src.DocumentID, AccessHash: src.AccessHash,
		FileReference: src.FileReference, Size: src.Size,
	}}
	return f.msgs[id].part, nil
}

func (f *fakeStore) Refresh(_ context.Context, messageID int) (*telegramstore.Part, error) {
	f.refreshCall++
	if f.refreshErr != nil {
		return nil, f.refreshErr
	}
	if f.afterRefresh != nil {
		f.afterRefresh()
	}
	return &telegramstore.Part{
		MessageID:     messageID,
		DocumentID:    7,
		AccessHash:    8,
		FileReference: f.refreshedRef,
		Size:          1,
	}, nil
}

func (f *fakeStore) ReadRange(_ context.Context, _ *telegramstore.Part, off int64, length int, progress func(int64)) ([]byte, error) {
	if f.readErr != nil {
		return nil, f.readErr
	}
	if progress != nil {
		progress(off + int64(length))
	}
	return f.readData, nil
}

func (f *fakeStore) Upload(context.Context, io.Reader, int64, string, string, string) (*telegramstore.Part, error) {
	return nil, errors.New("不应调用非续传上传")
}

func (f *fakeStore) UploadResumable(_ context.Context, _ io.ReaderAt, size int64, key, _ string, _ string, caption string) (*telegramstore.Part, error) {
	if f.uploadErr != nil {
		return nil, f.uploadErr
	}
	f.nextID++
	id := f.nextID
	f.uploadKeys = append(f.uploadKeys, key)
	f.uploadCaptions = append(f.uploadCaptions, caption)
	f.uploadSizes = append(f.uploadSizes, size)
	part := &telegramstore.Part{MessageID: id, DocumentID: int64(1000 + id), AccessHash: int64(id), Size: size}
	f.msgs[id] = nodeMsg{body: caption, part: part}
	return part, nil
}

func (f *fakeStore) Delete(_ context.Context, ids ...int) error {
	for _, id := range ids {
		delete(f.msgs, id)
	}
	f.deleted = append(f.deleted, ids...)
	return nil
}

// newTestFS 建一个临时索引与假存储；卷根是虚拟的，不需要根消息。
func newTestFS(t *testing.T) (*FileSystem, *fakeStore, func()) {
	t.Helper()
	fsys, st, _, cleanup := newTestFSWithEvents(t)
	return fsys, st, cleanup
}

// newTestFSWithEvents 同上，并返回推送记录器，用于验证写操作一定推送。
//
// 输入：t 测试
// 输出：文件系统、假存储、推送记录器、清理函数
// 预期：所有写路径都往记录器里写 status 主题
func newTestFSWithEvents(t *testing.T) (*FileSystem, *fakeStore, *push.Recorder, func()) {
	t.Helper()
	ctx := t.Context()
	idx, err := index.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	st := newFakeStore()
	rec := push.NewRecorder()
	return New(ctx, idx, st, 1<<20, rec), st, rec, func() { idx.Close() }
}

// putFile 通过写句柄上传一个文件，返回生成的消息号。
func putFile(t *testing.T, fsys *FileSystem, path, body string) int {
	t.Helper()
	ctx := t.Context()
	f, err := fsys.OpenFile(ctx, path, 1|2|0x40, 0o644)
	if err != nil {
		t.Fatalf("打开 %s: %v", path, err)
	}
	if _, err := f.Write([]byte(body)); err != nil {
		t.Fatalf("写 %s: %v", path, err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("关闭 %s: %v", path, err)
	}
	return nodeID(t, fsys, path)
}

// mkdir 建一个目录并返回消息号。
func mkdir(t *testing.T, fsys *FileSystem, path string) int {
	t.Helper()
	if err := fsys.Mkdir(t.Context(), path, 0o755); err != nil {
		t.Fatalf("建目录 %s: %v", path, err)
	}
	return nodeID(t, fsys, path)
}

// putRenameFixture 造 /d/f1.bin 与 /d/sub/f2.bin。
func putRenameFixture(t *testing.T, fsys *FileSystem) {
	t.Helper()
	mkdir(t, fsys, "/d")
	putFile(t, fsys, "/d/f1.bin", "aaa")
	mkdir(t, fsys, "/d/sub")
	putFile(t, fsys, "/d/sub/f2.bin", "bbbb")
}

// nodeID 返回路径对应节点的消息号。
func nodeID(t *testing.T, fsys *FileSystem, p string) int {
	t.Helper()
	n, ok, err := fsys.idx.NodeByPath(t.Context(), p)
	if err != nil || !ok {
		t.Fatalf("%s 未登记: ok=%v err=%v", p, ok, err)
	}
	return n.ID
}
