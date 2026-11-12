package index

// 已用空间统计测试。

import (
	"context"
	"testing"
)

// TestUsed 验证已用字节数按真实文件大小累计。
//
// 输入：空表 -> 写入 10 字节与 32 字节两条 -> 把 10 字节那条覆盖成 64
// 输出：0 -> 42 -> 96
// 预期：空表为 0；同路径覆盖后只算新值
func TestUsed(t *testing.T) {
	ctx := context.Background()
	idx, err := OpenMemory()
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer idx.Close()

	if n, err := idx.Used(ctx); err != nil || n != 0 {
		t.Fatalf("空表应 0，得 %d err=%v", n, err)
	}

	if err := idx.Put(ctx, File{Path: "/a.txt", MessageID: 1, Size: 10, Mtime: 1}); err != nil {
		t.Fatalf("put a: %v", err)
	}
	if err := idx.Put(ctx, File{Path: "/b.txt", MessageID: 2, Size: 32, Mtime: 1}); err != nil {
		t.Fatalf("put b: %v", err)
	}
	if n, err := idx.Used(ctx); err != nil || n != 42 {
		t.Fatalf("应 42，得 %d err=%v", n, err)
	}

	size := int64(64)
	if err := idx.Put(ctx, File{Path: "/a.txt", MessageID: 3, Size: size, Mtime: 2}); err != nil {
		t.Fatalf("put a2: %v", err)
	}
	if n, err := idx.Used(ctx); err != nil || n != 96 {
		t.Fatalf("应 96，得 %d err=%v", n, err)
	}
}
