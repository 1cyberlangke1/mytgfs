package dav

// COPY 语义测试：同卷复制走引用消息，不传输字节；目录按 Depth 递归。

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"golang.org/x/net/webdav"

	"mytgfs/internal/dirent"
)

// TestCopyPrefixFileCreatesReference 复制文件只发一条引用消息，不上传字节。
func TestCopyPrefixFileCreatesReference(t *testing.T) {
	fsys, st, done := newTestFS(t)
	defer done()
	ctx := t.Context()
	putRenameFixture(t, fsys)
	uploaded := len(st.uploadKeys)

	srcID := nodeID(t, fsys, "/d/f1.bin")
	src, ok, err := fsys.idx.NodeByID(ctx, srcID)
	if err != nil || !ok {
		t.Fatalf("源节点不存在: ok=%v err=%v", ok, err)
	}

	if err := fsys.CopyPrefix(ctx, "/d/f1.bin", "/x.bin", false, -1); err != nil {
		t.Fatalf("复制文件应成功: %v", err)
	}
	if len(st.uploadKeys) != uploaded {
		t.Fatalf("复制不应上传字节，实际 %v", st.uploadKeys)
	}
	if len(st.copiedSrc) != 1 || st.copiedSrc[0] != src.DocumentID {
		t.Fatalf("应引用 document %d，实际 %v", src.DocumentID, st.copiedSrc)
	}
	got, ok, err := fsys.idx.NodeByPath(ctx, "/x.bin")
	if err != nil || !ok || got.DocumentID != src.DocumentID || got.Sha256 != src.Sha256 {
		t.Fatalf("目标应登记为同一内容: ok=%v err=%v node=%+v", ok, err, got)
	}
	if _, ok, _ := fsys.idx.NodeByPath(ctx, "/d/f1.bin"); !ok {
		t.Fatal("源文件不应被改动")
	}
	// 新消息正文自述目标名字与卷根父号
	msg, exists := st.msgs[got.ID]
	if !exists {
		t.Fatal("目标节点消息应存在")
	}
	n, valid := dirent.Parse(msg.body)
	if !valid || n.Name != "x.bin" || n.Parent != 0 {
		t.Fatalf("目标节点正文不符: %+v (%s)", n, msg.body)
	}
}

// TestCopyPrefixDirectoryRecursive 整目录复制会复制每个文件并建立目录节点。
func TestCopyPrefixDirectoryRecursive(t *testing.T) {
	fsys, st, done := newTestFS(t)
	defer done()
	ctx := t.Context()
	putRenameFixture(t, fsys)

	if err := fsys.CopyPrefix(ctx, "/d", "/e", false, -1); err != nil {
		t.Fatalf("复制目录应成功: %v", err)
	}
	if len(st.copiedSrc) != 2 {
		t.Fatalf("两个文件都应复制，实际 %v", st.copiedSrc)
	}
	for _, p := range []string{"/e", "/e/f1.bin", "/e/sub", "/e/sub/f2.bin"} {
		if _, ok, err := fsys.idx.NodeByPath(ctx, p); err != nil || !ok {
			t.Fatalf("应存在 %s: ok=%v err=%v", p, ok, err)
		}
	}
}

// TestCopyPrefixDepthZero 目录 Depth 0 只建目录壳，不复制子项。
func TestCopyPrefixDepthZero(t *testing.T) {
	fsys, st, done := newTestFS(t)
	defer done()
	ctx := t.Context()
	putRenameFixture(t, fsys)

	if err := fsys.CopyPrefix(ctx, "/d", "/e", false, 0); err != nil {
		t.Fatalf("Depth 0 复制应成功: %v", err)
	}
	if len(st.copiedSrc) != 0 {
		t.Fatalf("Depth 0 不应复制文件，实际 %v", st.copiedSrc)
	}
	if n, ok, err := fsys.idx.NodeByPath(ctx, "/e"); err != nil || !ok || !n.IsDir {
		t.Fatalf("应建立空目录: ok=%v err=%v node=%+v", ok, err, n)
	}
	if _, ok, _ := fsys.idx.NodeByPath(ctx, "/e/f1.bin"); ok {
		t.Fatal("Depth 0 不应复制子文件")
	}
}

// TestCopyPrefixOverwriteRules 目标存在时按 overwrite 决定拒绝还是替换。
func TestCopyPrefixOverwriteRules(t *testing.T) {
	fsys, st, done := newTestFS(t)
	defer done()
	ctx := t.Context()
	putRenameFixture(t, fsys)
	oldID := putFile(t, fsys, "/x.bin", "zz")

	if err := fsys.CopyPrefix(ctx, "/d/f1.bin", "/x.bin", false, -1); !errors.Is(err, os.ErrExist) {
		t.Fatalf("不覆盖时应返回 ErrExist，实际 %v", err)
	}
	if err := fsys.CopyPrefix(ctx, "/d/f1.bin", "/x.bin", true, -1); err != nil {
		t.Fatalf("覆盖应成功: %v", err)
	}
	if len(st.deleted) != 1 || st.deleted[0] != oldID {
		t.Fatalf("覆盖应删掉旧消息 %d，实际 %v", oldID, st.deleted)
	}
	src, _, _ := fsys.idx.NodeByPath(ctx, "/d/f1.bin")
	got, _, _ := fsys.idx.NodeByPath(ctx, "/x.bin")
	if got.DocumentID != src.DocumentID {
		t.Fatalf("覆盖后应指向新内容: got=%+v src=%+v", got, src)
	}
}

// TestCopyPrefixRefusesOwnSubtree 复制进自己的子树会无限递归，必须拒绝。
func TestCopyPrefixRefusesOwnSubtree(t *testing.T) {
	fsys, _, done := newTestFS(t)
	defer done()
	ctx := t.Context()
	putRenameFixture(t, fsys)
	if err := fsys.CopyPrefix(ctx, "/d", "/d/sub/e", false, -1); err == nil {
		t.Fatal("复制进自身子树应报错")
	}
}

// TestHandlerCopyUsesReference COPY 请求走引用复制并返回 201。
func TestHandlerCopyUsesReference(t *testing.T) {
	fsys, st, done := newTestFS(t)
	defer done()
	ctx := t.Context()
	putRenameFixture(t, fsys)
	uploaded := len(st.uploadKeys)

	h := NewHandler(fsys, webdav.NewMemLS())
	req := httptest.NewRequest("COPY", "/d/f1.bin", nil)
	req.Header.Set("Destination", "/y.bin")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("COPY 应返回 201，实际 %d body=%s", rec.Code, rec.Body.String())
	}
	if len(st.uploadKeys) != uploaded {
		t.Fatalf("COPY 不应上传字节，实际 %v", st.uploadKeys)
	}
	if _, ok, _ := fsys.idx.NodeByPath(ctx, "/y.bin"); !ok {
		t.Fatal("COPY 后目标应存在")
	}
}

// TestHandlerCopyExistingReturns204 目标已存在且允许覆盖时返回 204。
func TestHandlerCopyExistingReturns204(t *testing.T) {
	fsys, _, done := newTestFS(t)
	defer done()
	putRenameFixture(t, fsys)

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
	putRenameFixture(t, fsys)

	h := NewHandler(fsys, webdav.NewMemLS())
	req := httptest.NewRequest("PROPFIND", "/d", nil)
	req.Header.Set("Depth", "1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMultiStatus {
		t.Fatalf("PROPFIND 应返回 207，实际 %d", rec.Code)
	}
}
