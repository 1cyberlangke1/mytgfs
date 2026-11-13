package dav

// COPY 语义测试：同卷复制走引用消息，不传输字节；目录按 Depth 递归。

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"golang.org/x/net/webdav"

	"mytgfs/internal/index"
)

// TestCopyPrefixFileCreatesReference 复制文件只发一条引用消息，不上传字节。
func TestCopyPrefixFileCreatesReference(t *testing.T) {
	fsys, st, done := newTestFS(t)
	defer done()
	ctx := context.Background()
	putRenameFixture(t, fsys, ctx)

	if err := fsys.CopyPrefix(ctx, "/d/f1.bin", "/x.bin", false, -1); err != nil {
		t.Fatalf("复制文件应成功: %v", err)
	}
	if len(st.uploadKeys) != 0 {
		t.Fatalf("复制不应上传字节，实际 %v", st.uploadKeys)
	}
	if len(st.copiedSrc) != 1 || st.copiedSrc[0] != 11 {
		t.Fatalf("应引用 document 11，实际 %v", st.copiedSrc)
	}
	got, ok, err := fsys.idx.Get(ctx, "/x.bin")
	if err != nil || !ok || got.DocumentID != 11 || got.Sha256 != "aa" {
		t.Fatalf("目标应登记为同一内容: ok=%v err=%v entry=%+v", ok, err, got)
	}
	if _, ok, _ := fsys.idx.Get(ctx, "/d/f1.bin"); !ok {
		t.Fatal("源文件不应被改动")
	}
}

// TestCopyPrefixDirectoryRecursive 整目录复制会复制每个文件并建立目录记录。
func TestCopyPrefixDirectoryRecursive(t *testing.T) {
	fsys, st, done := newTestFS(t)
	defer done()
	ctx := context.Background()
	putRenameFixture(t, fsys, ctx)

	if err := fsys.CopyPrefix(ctx, "/d", "/e", false, -1); err != nil {
		t.Fatalf("复制目录应成功: %v", err)
	}
	if len(st.copiedSrc) != 2 {
		t.Fatalf("两个文件都应复制，实际 %v", st.copiedSrc)
	}
	for _, p := range []string{"/e/f1.bin", "/e/sub/f2.bin"} {
		if _, ok, err := fsys.idx.Get(ctx, p); err != nil || !ok {
			t.Fatalf("应存在 %s: ok=%v err=%v", p, ok, err)
		}
	}
	if dirs, err := fsys.idx.Dirs(ctx, "/e"); err != nil || len(dirs) != 2 {
		t.Fatalf("目录记录应建立: dirs=%v err=%v", dirs, err)
	}
}

// TestCopyPrefixDepthZero 目录 Depth 0 只建目录，不复制子项。
func TestCopyPrefixDepthZero(t *testing.T) {
	fsys, st, done := newTestFS(t)
	defer done()
	ctx := context.Background()
	putRenameFixture(t, fsys, ctx)

	if err := fsys.CopyPrefix(ctx, "/d", "/e", false, 0); err != nil {
		t.Fatalf("Depth 0 复制应成功: %v", err)
	}
	if len(st.copiedSrc) != 0 {
		t.Fatalf("Depth 0 不应复制文件，实际 %v", st.copiedSrc)
	}
	if ok, err := fsys.idx.IsDir(ctx, "/e"); err != nil || !ok {
		t.Fatalf("应建立空目录: ok=%v err=%v", ok, err)
	}
}

// TestCopyPrefixOverwriteRules 目标存在时按 overwrite 决定拒绝还是替换。
func TestCopyPrefixOverwriteRules(t *testing.T) {
	fsys, st, done := newTestFS(t)
	defer done()
	ctx := context.Background()
	putRenameFixture(t, fsys, ctx)
	if err := fsys.idx.Put(ctx, index.File{Path: "/x.bin", MessageID: 20, DocumentID: 77, Size: 2}); err != nil {
		t.Fatal(err)
	}

	if err := fsys.CopyPrefix(ctx, "/d/f1.bin", "/x.bin", false, -1); !errors.Is(err, os.ErrExist) {
		t.Fatalf("不覆盖时应返回 ErrExist，实际 %v", err)
	}
	if err := fsys.CopyPrefix(ctx, "/d/f1.bin", "/x.bin", true, -1); err != nil {
		t.Fatalf("覆盖应成功: %v", err)
	}
	if len(st.deleted) != 1 || st.deleted[0] != 20 {
		t.Fatalf("覆盖应删掉旧消息 20，实际 %v", st.deleted)
	}
	got, _, _ := fsys.idx.Get(ctx, "/x.bin")
	if got.DocumentID != 11 {
		t.Fatalf("覆盖后应指向新内容，实际 %+v", got)
	}
}

// TestCopyPrefixRefusesOwnSubtree 复制进自己的子树会无限递归，必须拒绝。
func TestCopyPrefixRefusesOwnSubtree(t *testing.T) {
	fsys, _, done := newTestFS(t)
	defer done()
	ctx := context.Background()
	putRenameFixture(t, fsys, ctx)
	if err := fsys.CopyPrefix(ctx, "/d", "/d/sub/e", false, -1); err == nil {
		t.Fatal("复制进自身子树应报错")
	}
}

// TestHandlerCopyUsesReference COPY 请求走引用复制并返回 201。
func TestHandlerCopyUsesReference(t *testing.T) {
	fsys, st, done := newTestFS(t)
	defer done()
	putRenameFixture(t, fsys, t.Context())

	h := NewHandler(fsys, webdav.NewMemLS())
	req := httptest.NewRequest("COPY", "/d/f1.bin", nil)
	req.Header.Set("Destination", "/y.bin")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("COPY 应返回 201，实际 %d body=%s", rec.Code, rec.Body.String())
	}
	if len(st.uploadKeys) != 0 {
		t.Fatalf("COPY 不应上传字节，实际 %v", st.uploadKeys)
	}
	if _, ok, _ := fsys.idx.Get(t.Context(), "/y.bin"); !ok {
		t.Fatal("COPY 后目标应存在")
	}
}

// TestHandlerCopyExistingReturns204 目标已存在且允许覆盖时返回 204。
func TestHandlerCopyExistingReturns204(t *testing.T) {
	fsys, _, done := newTestFS(t)
	defer done()
	putRenameFixture(t, fsys, t.Context())

	h := NewHandler(fsys, webdav.NewMemLS())
	req := httptest.NewRequest("COPY", "/d/f1.bin", nil)
	req.Header.Set("Destination", "/d/sub/f2.bin")
	req.Header.Set("Overwrite", "T")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("覆盖 COPY 应返回 204，实际 %d", rec.Code)
	}
}

// TestHandlerNonCopyPassthrough 其它方法仍交给标准 handler（这里是 PROPFIND）。
func TestHandlerNonCopyPassthrough(t *testing.T) {
	fsys, _, done := newTestFS(t)
	defer done()
	putRenameFixture(t, fsys, t.Context())

	h := NewHandler(fsys, webdav.NewMemLS())
	req := httptest.NewRequest("PROPFIND", "/d", nil)
	req.Header.Set("Depth", "1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMultiStatus {
		t.Fatalf("PROPFIND 应返回 207，实际 %d", rec.Code)
	}
}
