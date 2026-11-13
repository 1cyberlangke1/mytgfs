package dav

// 下载进度测试：读取时进度进表，读完或关闭后清空。

import (
	"os"
	"testing"

	"mytgfs/internal/index"
)

// TestTransfersTracksRead 读一块就把已下载字节记进进度表。
//
// 输入：索引里一条 2MB 记录；读第一次 1KB
// 输出：无
// 预期：Transfers 有且只有这条，Done 等于已取回的分块字节；Close 后清空
func TestTransfersTracksRead(t *testing.T) {
	fsys, _, done := newTestFS(t)
	defer done()
	ctx := t.Context()
	const size = 2 << 20

	if err := fsys.idx.PutNode(ctx, index.Node{
		ID: 50, ParentID: 0, Name: "big.bin", Path: "/big.bin", DocumentID: 2, AccessHash: 3, Size: size,
	}); err != nil {
		t.Fatal(err)
	}
	f, err := fsys.OpenFile(ctx, "/big.bin", os.O_RDONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Read(make([]byte, 1024)); err != nil {
		t.Fatal(err)
	}
	got := fsys.Transfers()
	if len(got) != 1 {
		t.Fatalf("进度行数 = %d，期望 1", len(got))
	}
	if got[0].Path != "/big.bin" || got[0].Total != size || got[0].Done != fsys.chunk {
		t.Fatalf("进度 = %+v，期望 Done=%d Total=%d", got[0], fsys.chunk, size)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if left := fsys.Transfers(); len(left) != 0 {
		t.Fatalf("关闭后仍残留进度: %+v", left)
	}
}

// TestTransfersSkipsSmallFile 小文件不进进度表，避免面板闪烁。
func TestTransfersSkipsSmallFile(t *testing.T) {
	fsys, _, done := newTestFS(t)
	defer done()
	ctx := t.Context()

	if err := fsys.idx.PutNode(ctx, index.Node{
		ID: 51, ParentID: 0, Name: "small.bin", Path: "/small.bin", DocumentID: 2, AccessHash: 3, Size: 10,
	}); err != nil {
		t.Fatal(err)
	}
	f, err := fsys.OpenFile(ctx, "/small.bin", os.O_RDONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Read(make([]byte, 4)); err != nil {
		t.Fatal(err)
	}
	if got := fsys.Transfers(); len(got) != 0 {
		t.Fatalf("小文件不该有进度: %+v", got)
	}
	_ = f.Close()
}
