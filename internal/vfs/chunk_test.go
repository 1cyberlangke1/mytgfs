package vfs

// 读块大小运行期调整：设置页改完不需要重新挂卷。

import (
	"os"
	"strings"
	"testing"
)

// readOnce 按指定块长读一次 /a.bin 的第一个字节。
//
// 输入：ctx；fsys 卷；chunk 块长
// 输出：error
// 预期：只为触发一次 ReadRange，读完立即关闭句柄
func readOnce(t *testing.T, fsys *FileSystem, chunk int64) {
	t.Helper()
	ctx := t.Context()
	fsys.SetChunk(chunk)
	f, err := fsys.OpenFile(ctx, "/a.bin", os.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("打开 /a.bin: %v", err)
	}
	if _, err := f.Read(make([]byte, 1)); err != nil {
		_ = f.Close()
		t.Fatalf("读取: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("关闭: %v", err)
	}
}

// TestSetChunkAffectsNextRead 改块大小后下一次读取按新块长请求。
//
// 输入：文件 64 字节；先用 8 字节块读一次，改成 16 字节再读一次
// 输出：两次 ReadRange 请求的长度分别是 8 与 16
// 预期：块长在读取时才取值，不锁在构造参数里
func TestSetChunkAffectsNextRead(t *testing.T) {
	fsys, st, cleanup := newTestFS(t)
	defer cleanup()

	putFile(t, fsys, "/a.bin", strings.Repeat("x", 64))
	readOnce(t, fsys, 8)
	readOnce(t, fsys, 16)

	if len(st.readLens) != 2 || st.readLens[0] != 8 || st.readLens[1] != 16 {
		t.Fatalf("ReadRange 长度 = %v，期望 [8 16]", st.readLens)
	}
}

// TestSetChunkDefaults 非正数回到 1MB，避免 0 块长把读循环卡住。
func TestSetChunkDefaults(t *testing.T) {
	fsys, _, cleanup := newTestFS(t)
	defer cleanup()

	fsys.SetChunk(0)
	if got := fsys.chunk.Load(); got != 1<<20 {
		t.Fatalf("chunk = %d，期望 1MB", got)
	}
}
