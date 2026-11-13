package index

// 已上传待索引记录（pending_done）的读写测试。
//
// 用途：上传成功但索引登记失败时，靠这张表在重启后补索引，不重传字节。

import (
	"bytes"
	"context"
	"testing"
)

// TestPendingDoneRoundTrip 记录写入后能原样读回，删除后查不到，删除不存在不报错。
func TestPendingDoneRoundTrip(t *testing.T) {
	idx, err := OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()
	ctx := context.Background()

	ref := []byte("file-ref-1")
	pd := PendingDone{
		Path: "/a.bin", MessageID: 42, DocumentID: 900, AccessHash: -7,
		FileReference: ref, Size: 5, Sha256: "abc", Mtime: 11,
	}
	if err := idx.PutPendingDone(ctx, pd); err != nil {
		t.Fatal(err)
	}
	got, ok, err := idx.GetPendingDone(ctx, "/a.bin")
	if err != nil || !ok {
		t.Fatalf("应能读到记录: ok=%v err=%v", ok, err)
	}
	if got.MessageID != 42 || got.DocumentID != 900 || got.AccessHash != -7 ||
		got.Size != 5 || got.Sha256 != "abc" || got.Mtime != 11 ||
		!bytes.Equal(got.FileReference, ref) {
		t.Fatalf("读回内容不一致: %+v", got)
	}

	if err := idx.DeletePendingDone(ctx, "/a.bin"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := idx.GetPendingDone(ctx, "/a.bin"); err != nil || ok {
		t.Fatalf("删除后不应命中: ok=%v err=%v", ok, err)
	}
	if err := idx.DeletePendingDone(ctx, "/nope"); err != nil {
		t.Fatalf("删除不存在的记录应幂等: %v", err)
	}
}
