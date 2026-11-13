package dav

// 目录删除测试：WebDAV 客户端对集合发 DELETE 时只会调用 RemoveAll，
// 因此这里必须递归删掉子文件的消息与索引，否则界面显示删了、内容其实还在。

import (
	"context"
	"errors"
	"os"
	"testing"

	"mytgfs/internal/index"
)

// TestRemoveAllDirectoryRemovesSubtree 删目录时子文件的消息与索引一起清掉。
func TestRemoveAllDirectoryRemovesSubtree(t *testing.T) {
	fsys, st, done := newTestFS(t)
	defer done()
	ctx := context.Background()
	putRenameFixture(t, fsys, ctx)

	if err := fsys.RemoveAll(ctx, "/d"); err != nil {
		t.Fatalf("删目录应成功: %v", err)
	}
	if len(st.deleted) != 2 {
		t.Fatalf("两个子文件的消息都应删除，实际 %v", st.deleted)
	}
	for _, p := range []string{"/d/f1.bin", "/d/sub/f2.bin"} {
		if _, ok, err := fsys.idx.Get(ctx, p); err != nil || ok {
			t.Fatalf("%s 索引应删除: ok=%v err=%v", p, ok, err)
		}
	}
	if dirs, err := fsys.idx.Dirs(ctx, "/d"); err != nil || len(dirs) != 0 {
		t.Fatalf("目录记录应清空: dirs=%v err=%v", dirs, err)
	}
}

// TestRemoveAllFileDeletesMessage 单文件删除仍只删自己的消息与索引。
func TestRemoveAllFileDeletesMessage(t *testing.T) {
	fsys, st, done := newTestFS(t)
	defer done()
	ctx := context.Background()
	putRenameFixture(t, fsys, ctx)

	if err := fsys.RemoveAll(ctx, "/d/f1.bin"); err != nil {
		t.Fatalf("删文件应成功: %v", err)
	}
	if len(st.deleted) != 1 || st.deleted[0] != 1 {
		t.Fatalf("应只删消息 1，实际 %v", st.deleted)
	}
	if _, ok, _ := fsys.idx.Get(ctx, "/d/sub/f2.bin"); !ok {
		t.Fatal("同目录其它文件不应受影响")
	}
}

// TestRemoveAllKeepsRoot 根目录仍拒绝删除。
func TestRemoveAllKeepsRoot(t *testing.T) {
	fsys, st, done := newTestFS(t)
	defer done()
	ctx := context.Background()
	if err := fsys.idx.Put(ctx, index.File{Path: "/x.bin", MessageID: 3, Size: 1}); err != nil {
		t.Fatal(err)
	}
	if err := fsys.RemoveAll(ctx, "/"); err == nil {
		t.Fatal("根目录删除应报错")
	}
	if len(st.deleted) != 0 {
		t.Fatalf("被拒绝时不应删任何消息，实际 %v", st.deleted)
	}
	if _, ok, _ := fsys.idx.Get(ctx, "/x.bin"); !ok {
		t.Fatal("被拒绝时索引不应变动")
	}
}

// TestRemoveAllMissingPath 不存在的路径应表现为 ErrNotExist，而不是静默成功。
func TestRemoveAllMissingPath(t *testing.T) {
	fsys, _, done := newTestFS(t)
	defer done()
	err := fsys.RemoveAll(context.Background(), "/nope.bin")
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("不存在应返回 ErrNotExist，实际 %v", err)
	}
}
