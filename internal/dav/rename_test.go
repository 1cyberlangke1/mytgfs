package dav

// 目录改名测试：映射盘里重命名文件夹要真正迁移整棵子树，而不是直接报不支持。

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"mytgfs/internal/index"
)

// renameCalls 记录一次改名调用的消息号与 caption。
type renameCall struct {
	id      int
	caption string
}

// TestRenameDirectoryMovesSubtree 目录改名后所有子文件的消息说明与索引一起迁移。
func TestRenameDirectoryMovesSubtree(t *testing.T) {
	fsys, st, done := newTestFS(t)
	defer done()
	ctx := context.Background()
	putRenameFixture(t, fsys, ctx)

	if err := fsys.Rename(ctx, "/d", "/e"); err != nil {
		t.Fatalf("目录改名应成功: %v", err)
	}

	if len(st.renames) != 2 {
		t.Fatalf("两个文件都要改说明，实际 %+v", st.renames)
	}
	for _, rc := range st.renames {
		if !strings.Contains(rc.caption, `"path":"/e/`) {
			t.Fatalf("caption 应带新路径，实际 %s", rc.caption)
		}
	}
	for _, p := range []string{"/e/f1.bin", "/e/sub/f2.bin"} {
		if _, ok, err := fsys.idx.Get(ctx, p); err != nil || !ok {
			t.Fatalf("改名后应存在 %s: ok=%v err=%v", p, ok, err)
		}
	}
	for _, p := range []string{"/d/f1.bin", "/d/sub/f2.bin"} {
		if _, ok, _ := fsys.idx.Get(ctx, p); ok {
			t.Fatalf("旧路径应消失: %s", p)
		}
	}
	if dirs, err := fsys.idx.Dirs(ctx, "/e"); err != nil || len(dirs) != 2 {
		t.Fatalf("目录记录应迁到 /e 下: dirs=%v err=%v", dirs, err)
	}
	if dirs, err := fsys.idx.Dirs(ctx, "/d"); err != nil || len(dirs) != 0 {
		t.Fatalf("旧目录记录应清空: dirs=%v err=%v", dirs, err)
	}
}

// TestRenameEmptyDirectory 空目录（只有 dirs 记录）也能改名。
func TestRenameEmptyDirectory(t *testing.T) {
	fsys, _, done := newTestFS(t)
	defer done()
	ctx := context.Background()
	if err := fsys.idx.PutDir(ctx, "/empty"); err != nil {
		t.Fatal(err)
	}
	if err := fsys.Rename(ctx, "/empty", "/empty2"); err != nil {
		t.Fatalf("空目录改名应成功: %v", err)
	}
	if ok, err := fsys.idx.IsDir(ctx, "/empty2"); err != nil || !ok {
		t.Fatalf("新目录应存在: ok=%v err=%v", ok, err)
	}
	if ok, _ := fsys.idx.IsDir(ctx, "/empty"); ok {
		t.Fatal("旧目录记录应删除")
	}
}

// TestRenameDirectoryRejectsOwnSubtree 移进自己的子树会形成环，必须拒绝。
func TestRenameDirectoryRejectsOwnSubtree(t *testing.T) {
	fsys, st, done := newTestFS(t)
	defer done()
	ctx := context.Background()
	putRenameFixture(t, fsys, ctx)

	if err := fsys.Rename(ctx, "/d", "/d/sub/d2"); err == nil {
		t.Fatal("移进自身子树应报错")
	}
	if len(st.renames) != 0 {
		t.Fatalf("被拒绝时不应改任何消息，实际 %+v", st.renames)
	}
	if _, ok, _ := fsys.idx.Get(ctx, "/d/f1.bin"); !ok {
		t.Fatal("被拒绝时索引不应变动")
	}
}

// TestRenameDirectoryRejectsExistingTarget 目标已存在时拒绝，避免覆盖。
func TestRenameDirectoryRejectsExistingTarget(t *testing.T) {
	fsys, _, done := newTestFS(t)
	defer done()
	ctx := context.Background()
	putRenameFixture(t, fsys, ctx)
	if err := fsys.idx.Put(ctx, index.File{Path: "/other/x.bin", MessageID: 9, Size: 1}); err != nil {
		t.Fatal(err)
	}

	err := fsys.Rename(ctx, "/d", "/other")
	if !errors.Is(err, os.ErrExist) {
		t.Fatalf("目标存在时应返回 ErrExist，实际 %v", err)
	}
	if _, ok, _ := fsys.idx.Get(ctx, "/d/f1.bin"); !ok {
		t.Fatal("被拒绝时索引不应变动")
	}
}

// TestRenameRootRejected 根目录不能改名。
func TestRenameRootRejected(t *testing.T) {
	fsys, _, done := newTestFS(t)
	defer done()
	if err := fsys.Rename(context.Background(), "/", "/x"); err == nil {
		t.Fatal("根目录改名应报错")
	}
}

// putRenameFixture 造一棵 /d 子树：/d/f1.bin 与 /d/sub/f2.bin。
func putRenameFixture(t *testing.T, fsys *FileSystem, ctx context.Context) {
	t.Helper()
	for _, f := range []index.File{
		{Path: "/d/f1.bin", MessageID: 1, DocumentID: 11, Size: 3, Sha256: "aa"},
		{Path: "/d/sub/f2.bin", MessageID: 2, DocumentID: 12, Size: 4, Sha256: "bb"},
	} {
		if err := fsys.idx.Put(ctx, f); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []string{"/d", "/d/sub"} {
		if err := fsys.idx.PutDir(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
}
