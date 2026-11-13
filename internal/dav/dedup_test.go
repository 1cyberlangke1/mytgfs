package dav

// 写时去重复用回归测试：同内容文件只上传一次，第二个路径复用已有 document。

import (
	"context"
	"os"
	"testing"

	"mytgfs/internal/dirent"
	"mytgfs/internal/index"
)

// seedContent 在卷根登记一个已知内容的文件节点。
func seedContent(t *testing.T, fsys *FileSystem, name string, id int, documentID int64) {
	t.Helper()
	if err := fsys.idx.PutNode(t.Context(), index.Node{
		ID: id, ParentID: 0, Name: name, Path: "/" + name,
		DocumentID: documentID, AccessHash: 8, Size: 5, Sha256: helloSHA,
	}); err != nil {
		t.Fatal(err)
	}
}

// TestWriteReusesExistingContent 同内容新文件不重传字节，直接引用已有 document。
func TestWriteReusesExistingContent(t *testing.T) {
	fsys, st, done := newTestFS(t)
	defer done()
	ctx := t.Context()
	seedContent(t, fsys, "a.bin", 10, 777)

	f, err := fsys.OpenFile(ctx, "/b.bin", os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	if len(st.uploadKeys) != 0 {
		t.Fatalf("命中去重时不应调用上传，实际 %v", st.uploadKeys)
	}
	if len(st.copiedSrc) != 1 || st.copiedSrc[0] != 777 {
		t.Fatalf("应复用 document 777，实际 %v", st.copiedSrc)
	}
	got, ok, _ := fsys.idx.NodeByPath(ctx, "/b.bin")
	if !ok || got.Sha256 != helloSHA || got.DocumentID != 777 {
		t.Fatalf("新路径应登记为同内容: ok=%v node=%+v", ok, got)
	}
	msg, exists := st.msgs[got.ID]
	node, valid := dirent.Parse(msg.body)
	if !exists || !valid || node.Name != "b.bin" || node.Sha != helloSHA {
		t.Fatalf("引用消息正文应自述 b.bin 与摘要: exists=%v %q", exists, msg.body)
	}
}

// TestWriteDifferentContentUploads 内容不同不去重，仍走正常上传。
func TestWriteDifferentContentUploads(t *testing.T) {
	fsys, st, done := newTestFS(t)
	defer done()
	ctx := t.Context()
	seedContent(t, fsys, "a.bin", 10, 777)

	f, err := fsys.OpenFile(ctx, "/b.bin", os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("world")); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if len(st.uploadKeys) != 1 {
		t.Fatalf("不同内容应上传，实际 %v", st.uploadKeys)
	}
	if len(st.copiedSrc) != 0 {
		t.Fatalf("不同内容不应复用，实际 %v", st.copiedSrc)
	}
}

// TestWriteDeduplicationCopyFailure 复用失败时应回退到正常上传，不丢数据。
func TestWriteDeduplicationCopyFailure(t *testing.T) {
	fsys, st, done := newTestFS(t)
	defer done()
	ctx := t.Context()
	seedContent(t, fsys, "a.bin", 10, 777)
	st.copyErr = context.DeadlineExceeded

	f, err := fsys.OpenFile(ctx, "/b.bin", os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("复用失败应回退上传而不是报错: %v", err)
	}
	if len(st.uploadKeys) != 1 {
		t.Fatalf("回退应产生一次上传，实际 %v", st.uploadKeys)
	}
	if _, ok, _ := fsys.idx.NodeByPath(ctx, "/b.bin"); !ok {
		t.Fatal("回退上传后新路径应登记索引")
	}
}
