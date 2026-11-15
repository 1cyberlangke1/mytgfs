package vfs

// 引用刷新只更新文档字段：不能把同步循环刚应用的改名覆盖回去。

import (
	"bytes"
	"context"
	"os"
	"testing"

	"github.com/gotd/td/tgerr"

	"mytgfs/internal/index"
)

// TestRefreshKeepsRenamedPath 句柄持有旧快照时刷新引用，名字与路径仍以库里最新状态为准。
func TestRefreshKeepsRenamedPath(t *testing.T) {
	fsys, st, done := newTestFS(t)
	defer done()
	ctx := context.Background()

	if err := fsys.idx.PutNode(ctx, index.Node{
		ID: 42, ParentID: 0, Name: "a.bin", Path: "/a.bin",
		DocumentID: 7, AccessHash: 8, FileReference: []byte("old"), Size: 1,
	}); err != nil {
		t.Fatal(err)
	}

	// 句柄先打开，持有改名前的快照
	f, err := fsys.OpenFile(ctx, "/a.bin", os.O_RDONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	// 同步循环把这条消息改名了（远端权威）
	if _, _, err := fsys.idx.MoveNode(ctx, 42, 0, "b.bin"); err != nil {
		t.Fatal(err)
	}

	st.readErr = tgerr.New(400, "FILE_REFERENCE_EXPIRED")
	st.refreshedRef = []byte("new-ref")
	st.afterRefresh = func() { st.readErr = nil }

	if _, err := f.Read(make([]byte, 1)); err != nil {
		t.Fatalf("刷新后应能读回: %v", err)
	}

	n, ok, err := fsys.idx.NodeByID(ctx, 42)
	if err != nil || !ok {
		t.Fatalf("节点丢失: ok=%v err=%v", ok, err)
	}
	if n.Name != "b.bin" || n.Path != "/b.bin" {
		t.Fatalf("刷新把改名覆盖回去了: %+v", n)
	}
	if !bytes.Equal(n.FileReference, []byte("new-ref")) {
		t.Fatalf("新引用未写回: %q", n.FileReference)
	}
}
