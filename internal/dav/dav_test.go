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
	nextID         int
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
