package dav

// WebDAV 层单测：用假存储代替 Telegram，验证写入、断点键、sha256 与外部状态处理。

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gotd/td/tgerr"

	"mytgfs/internal/index"
	"mytgfs/internal/telegramstore"
)

// fakeStore 记录上传参数并可按需返回读取错误。
type fakeStore struct {
	uploadKeys     []string
	uploadCaptions []string
	uploadSizes    []int64
	deleted        []int
	readErr        error
	uploadErr      error
	nextID         int
	refreshCalls   int
	refreshedRef   []byte
	refreshErr     error
	afterRefresh   func()
	copiedSrc      []int64
	copyErr        error
	copyPart       *telegramstore.Part
}

// Copy 记录被复用的 document ID，返回预置 Part 或默认新消息。
func (f *fakeStore) Copy(_ context.Context, src *telegramstore.Part, _, _, _ string) (*telegramstore.Part, error) {
	if f.copyErr != nil {
		return nil, f.copyErr
	}
	f.copiedSrc = append(f.copiedSrc, src.DocumentID)
	if f.copyPart != nil {
		return f.copyPart, nil
	}
	f.nextID++
	return &telegramstore.Part{
		MessageID:     f.nextID,
		DocumentID:    src.DocumentID,
		AccessHash:    src.AccessHash,
		FileReference: src.FileReference,
		Size:          src.Size,
	}, nil
}

// Refresh 返回预置的新引用，并可选地在刷新后改变读取行为。
func (f *fakeStore) Refresh(_ context.Context, messageID int) (*telegramstore.Part, error) {
	f.refreshCalls++
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

func (f *fakeStore) ReadRange(context.Context, *telegramstore.Part, int64, int, func(int64)) ([]byte, error) {
	if f.readErr != nil {
		return nil, f.readErr
	}
	return []byte("x"), nil
}

func (f *fakeStore) Upload(context.Context, io.Reader, int64, string, string, string) (*telegramstore.Part, error) {
	return nil, errors.New("不应调用非续传上传")
}

func (f *fakeStore) UploadResumable(_ context.Context, _ io.ReaderAt, size int64, key, _ string, _ string, caption string) (*telegramstore.Part, error) {
	if f.uploadErr != nil {
		return nil, f.uploadErr
	}
	f.nextID++
	f.uploadKeys = append(f.uploadKeys, key)
	f.uploadCaptions = append(f.uploadCaptions, caption)
	f.uploadSizes = append(f.uploadSizes, size)
	return &telegramstore.Part{
		MessageID:  f.nextID,
		DocumentID: int64(1000 + f.nextID),
		AccessHash: int64(f.nextID),
		Size:       size,
	}, nil
}

func (f *fakeStore) Delete(_ context.Context, ids ...int) error {
	f.deleted = append(f.deleted, ids...)
	return nil
}

func (f *fakeStore) Rename(context.Context, int, string) error { return nil }

// newTestFS 建一个临时索引与假存储。
func newTestFS(t *testing.T) (*FileSystem, *fakeStore, func()) {
	t.Helper()
	idx, err := index.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	st := &fakeStore{}
	return New(context.Background(), idx, st, 1<<20), st, func() { idx.Close() }
}

// TestWriteStoresHashAndCaption 写入后索引与 caption 都带内容 sha256。
func TestWriteStoresHashAndCaption(t *testing.T) {
	fsys, st, done := newTestFS(t)
	defer done()

	f, err := fsys.OpenFile(context.Background(), "/a.bin", os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	want := "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if len(st.uploadKeys) != 1 || st.uploadKeys[0] != want {
		t.Fatalf("断点键应为内容 sha256，实际 %v", st.uploadKeys)
	}
	if !strings.Contains(st.uploadCaptions[0], want) {
		t.Fatalf("caption 应带 sha256，实际 %s", st.uploadCaptions[0])
	}
	entry, ok, err := fsys.idx.Get(context.Background(), "/a.bin")
	if err != nil || !ok {
		t.Fatalf("索引应写入: %v %v", err, ok)
	}
	if entry.Sha256 != want || entry.Size != 5 {
		t.Fatalf("索引内容不符: %+v", entry)
	}
}

// TestOverwriteDeletesOldMessage 覆盖写会删掉同路径的旧消息。
func TestOverwriteDeletesOldMessage(t *testing.T) {
	fsys, st, done := newTestFS(t)
	defer done()

	for _, body := range []string{"first", "second"} {
		f, err := fsys.OpenFile(context.Background(), "/a.bin", os.O_WRONLY|os.O_CREATE, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if len(st.deleted) != 1 || st.deleted[0] != 1 {
		t.Fatalf("应删除旧消息 1，实际 %v", st.deleted)
	}
}

// TestReadRemovesDeletedMessage 远端消息被删时按文件不存在处理并清索引。
func TestReadRemovesDeletedMessage(t *testing.T) {
	fsys, st, done := newTestFS(t)
	defer done()

	if err := fsys.idx.Put(context.Background(), index.File{Path: "/gone.bin", MessageID: 7, Size: 10}); err != nil {
		t.Fatal(err)
	}
	st.readErr = tgerr.New(400, "MESSAGE_ID_INVALID")
	f, err := fsys.OpenFile(context.Background(), "/gone.bin", os.O_RDONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.Read(make([]byte, 4))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("应返回文件不存在，实际 %v", err)
	}
	if _, ok, _ := fsys.idx.Get(context.Background(), "/gone.bin"); ok {
		t.Fatal("索引里的记录应被清掉")
	}
}

// TestReadOtherErrorPropagates 其它读取错误原样返回，不动索引。
func TestReadOtherErrorPropagates(t *testing.T) {
	fsys, st, done := newTestFS(t)
	defer done()

	if err := fsys.idx.Put(context.Background(), index.File{Path: "/x.bin", MessageID: 8, Size: 10}); err != nil {
		t.Fatal(err)
	}
	st.readErr = errors.New("network down")
	f, err := fsys.OpenFile(context.Background(), "/x.bin", os.O_RDONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Read(make([]byte, 4)); err == nil {
		t.Fatal("应返回读取错误")
	}
	if _, ok, _ := fsys.idx.Get(context.Background(), "/x.bin"); !ok {
		t.Fatal("索引不应被删")
	}
}

// TestReadReturnsData 正常读取返回存储层数据。
func TestReadReturnsData(t *testing.T) {
	fsys, st, done := newTestFS(t)
	defer done()
	if err := fsys.idx.Put(context.Background(), index.File{Path: "/d.bin", MessageID: 9, Size: 1}); err != nil {
		t.Fatal(err)
	}
	f, err := fsys.OpenFile(context.Background(), "/d.bin", os.O_RDONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	n, err := f.Read(buf)
	if err != nil || n != 1 || !bytes.Equal(buf[:n], []byte("x")) {
		t.Fatalf("读取结果不符: n=%d err=%v", n, err)
	}
	_ = st
}

// TestSweepTempRemovesOnlyStale 只删过期且名字匹配的临时文件。
//
// 输入：目录内 4 个文件（旧匹配 / 新匹配 / 旧不匹配 / 子目录）
// 输出：返回删除数 1；其余三个仍存在
// 预期：不碰非本程序命名规则的文件与目录
func TestSweepTempRemovesOnlyStale(t *testing.T) {
	dir := t.TempDir()
	old := time.Now().Add(-48 * time.Hour)

	stale := filepath.Join(dir, ".mytgfs-dav-stale")
	fresh := filepath.Join(dir, ".mytgfs-dav-fresh")
	other := filepath.Join(dir, "keep.txt")
	for _, p := range []string{stale, fresh, other} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(other, old, old); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, ".mytgfs-dav-dir")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(sub, old, old); err != nil {
		t.Fatal(err)
	}

	n, err := SweepTemp(dir, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("应只删 1 个过期临时文件，实际 %d", n)
	}
	for _, p := range []string{fresh, other, sub} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("%s 不应被删: %v", p, err)
		}
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("过期临时文件应已删除: %v", err)
	}
}

// TestSweepTempMissingDir 目录不存在时视为没有残留。
func TestSweepTempMissingDir(t *testing.T) {
	n, err := SweepTemp(filepath.Join(t.TempDir(), "nope"), time.Hour)
	if err != nil || n != 0 {
		t.Fatalf("目录不存在应返回 0,nil，实际 %d,%v", n, err)
	}
}

// TestNewTempUsesConfiguredDir 临时文件落在 SetTempDir 指定的目录，目录自动创建。
func TestNewTempUsesConfiguredDir(t *testing.T) {
	fsys, _, done := newTestFS(t)
	defer done()
	dir := filepath.Join(t.TempDir(), "deep", "tmp")
	fsys.SetTempDir(dir)

	f, err := fsys.OpenFile(context.Background(), "/t.bin", os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	name := f.(*file).tmp.Name()
	t.Cleanup(func() { _ = f.Close() })
	if filepath.Dir(name) != dir {
		t.Fatalf("临时文件应在 %s，实际 %s", dir, name)
	}
}
