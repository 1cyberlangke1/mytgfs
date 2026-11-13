package dav

// file_reference 过期刷新的回归测试：重建索引拿到的是历史引用，读取时须刷新后重试。

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/gotd/td/tgerr"

	"mytgfs/internal/index"
)

// TestReadRefreshesExpiredFileReference 远端报引用过期时，刷新元数据并重试读取成功。
func TestReadRefreshesExpiredFileReference(t *testing.T) {
	fsys, st, done := newTestFS(t)
	defer done()
	ctx := context.Background()

	old := []byte("old-ref")
	if err := fsys.idx.PutNode(ctx, index.Node{
		ID: 42, ParentID: 0, Name: "a.bin", Path: "/a.bin",
		DocumentID: 7, AccessHash: 8, FileReference: old, Size: 1,
	}); err != nil {
		t.Fatal(err)
	}
	// 第一次读取报引用过期；刷新返回新引用后第二次读取成功
	st.readErr = tgerr.New(400, "FILE_REFERENCE_EXPIRED")
	st.refreshedRef = []byte("new-ref")
	st.afterRefresh = func() { st.readErr = nil }

	f, err := fsys.OpenFile(ctx, "/a.bin", os.O_RDONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	buf := make([]byte, 1)
	if _, err := f.Read(buf); err != nil {
		t.Fatalf("刷新后应能读回: %v", err)
	}
	if string(buf) != "x" {
		t.Fatalf("读取内容不符: %q", buf)
	}
	if st.refreshCall != 1 {
		t.Fatalf("应刷新一次，实际 %d", st.refreshCall)
	}
	got, _, _ := fsys.idx.NodeByID(ctx, 42)
	if string(got.FileReference) != "new-ref" {
		t.Fatalf("刷新后的引用应写回索引，实际 %q", got.FileReference)
	}
}

// TestReadDoesNotRefreshOnOtherError 非引用错误不刷新，原样返回。
func TestReadDoesNotRefreshOnOtherError(t *testing.T) {
	fsys, st, done := newTestFS(t)
	defer done()
	ctx := context.Background()

	if err := fsys.idx.PutNode(ctx, index.Node{
		ID: 42, ParentID: 0, Name: "a.bin", Path: "/a.bin", Size: 1,
	}); err != nil {
		t.Fatal(err)
	}
	st.readErr = errors.New("network down")

	f, err := fsys.OpenFile(ctx, "/a.bin", os.O_RDONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Read(make([]byte, 1)); err == nil {
		t.Fatal("应返回读取错误")
	}
	if st.refreshCall != 0 {
		t.Fatalf("非引用错误不应刷新，实际 %d", st.refreshCall)
	}
}
