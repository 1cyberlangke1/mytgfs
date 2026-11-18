package vfs

import (
	"os"
	"strings"
	"testing"
)

// TestBlockCacheHit 验证同一块第二次读取命中缓存。
//
// 输入：同一 key 存一次再取一次
// 输出：拿到相同内容，块数/字节数只算一份
// 预期：命中不会增加占用
func TestBlockCacheHit(t *testing.T) {
	c := newBlockCache(1 << 20)
	key := blockKey{id: 7, index: 0, length: 3}
	c.Put(key, []byte("abc"))

	got, ok := c.Get(key)
	if !ok || string(got) != "abc" {
		t.Fatalf("Get = %q, %v; want abc, true", got, ok)
	}
	blocks, bytes := c.Len()
	if blocks != 1 || bytes != 3 {
		t.Fatalf("Len = %d,%d; want 1,3", blocks, bytes)
	}
}

// TestBlockCachePartialBlock 验证短块也算命中。
//
// 输入：存 3 字节（文件尾块）
// 输出：命中且原样返回
// 预期：尾块本来就不满一个块长，读取方按实际字节消费
func TestBlockCachePartialBlock(t *testing.T) {
	c := newBlockCache(1 << 20)
	key := blockKey{id: 1, index: 2, length: 3}
	c.Put(key, []byte("abc"))

	got, ok := c.Get(key)
	if !ok || string(got) != "abc" {
		t.Fatalf("Get = %q, %v; want abc, true", got, ok)
	}
}

// TestBlockCacheEvict 验证超出字节上限时淘汰最旧块。
//
// 输入：上限 2 块，依次存 3 块
// 输出：第一块被淘汰，后两块可读
// 预期：占用始终不超过上限
func TestBlockCacheEvict(t *testing.T) {
	c := newBlockCache(4)
	c.Put(blockKey{id: 1, index: 0, length: 2}, []byte("aa"))
	c.Put(blockKey{id: 1, index: 1, length: 2}, []byte("bb"))
	c.Put(blockKey{id: 1, index: 2, length: 2}, []byte("cc"))

	if _, ok := c.Get(blockKey{id: 1, index: 0, length: 2}); ok {
		t.Fatal("最旧块未被淘汰")
	}
	for _, idx := range []int64{1, 2} {
		if _, ok := c.Get(blockKey{id: 1, index: idx, length: 2}); !ok {
			t.Fatalf("块 %d 应当还在", idx)
		}
	}
	if blocks, bytes := c.Len(); blocks != 2 || bytes != 4 {
		t.Fatalf("Len = %d,%d; want 2,4", blocks, bytes)
	}
}

// TestBlockCacheDrop 验证按节点丢弃块。
//
// 输入：两个节点各一块，丢弃其中一个
// 输出：目标节点的块消失，另一个保留
// 预期：删除文件后不必等淘汰就释放内存
func TestBlockCacheDrop(t *testing.T) {
	c := newBlockCache(1 << 20)
	c.Put(blockKey{id: 1, index: 0, length: 2}, []byte("aa"))
	c.Put(blockKey{id: 2, index: 0, length: 2}, []byte("bb"))

	c.Drop(1)

	if _, ok := c.Get(blockKey{id: 1, index: 0, length: 2}); ok {
		t.Fatal("节点 1 的块未丢弃")
	}
	if _, ok := c.Get(blockKey{id: 2, index: 0, length: 2}); !ok {
		t.Fatal("节点 2 的块被误删")
	}
	if blocks, bytes := c.Len(); blocks != 1 || bytes != 2 {
		t.Fatalf("Len = %d,%d; want 1,2", blocks, bytes)
	}
}

// TestBlockCacheTooLarge 验证超过上限的单块直接不缓存。
//
// 输入：上限 4 字节，存 8 字节
// 输出：不命中且占用为 0
// 预期：避免一个超大块把整个缓存挤空
func TestBlockCacheTooLarge(t *testing.T) {
	c := newBlockCache(4)
	c.Put(blockKey{id: 1, index: 0, length: 8}, []byte("12345678"))

	if blocks, bytes := c.Len(); blocks != 0 || bytes != 0 {
		t.Fatalf("Len = %d,%d; want 0,0", blocks, bytes)
	}
}

// TestBlockCacheSharedAcrossHandles 验证两个句柄读同一块只访问远端一次。
//
// 输入：同一文件先后开两个只读句柄，各读首字节
// 输出：ReadRange 只被调用一次（长度为块长）
// 预期：每个 /api/file 窗口都会新开句柄，块缓存不能挂在句柄上，
// 否则同一段数据会被反复从 Telegram 下载
func TestBlockCacheSharedAcrossHandles(t *testing.T) {
	fsys, st, cleanup := newTestFS(t)
	defer cleanup()

	putFile(t, fsys, "/shared.bin", strings.Repeat("y", 64))
	// fakeStore 按 readData 原样返回；给它一个满块长度，模拟真实 ReadRange
	st.readData = []byte(strings.Repeat("x", 16))
	fsys.SetChunk(16)

	for i := 0; i < 2; i++ {
		f, err := fsys.OpenFile(t.Context(), "/shared.bin", os.O_RDONLY, 0)
		if err != nil {
			t.Fatalf("第 %d 次打开: %v", i+1, err)
		}
		buf := make([]byte, 1)
		if _, err := f.Read(buf); err != nil {
			_ = f.Close()
			t.Fatalf("第 %d 次读取: %v", i+1, err)
		}
		// fakeStore 每次返回同一份 readData，这里只校验拿到了首字节
		if len(st.readData) == 0 || buf[0] != st.readData[0] {
			_ = f.Close()
			t.Fatalf("第 %d 次读到 %q，期望 %q", i+1, buf[0], st.readData[0])
		}
		if err := f.Close(); err != nil {
			t.Fatalf("第 %d 次关闭: %v", i+1, err)
		}
	}

	if len(st.readLens) != 1 || st.readLens[0] != 16 {
		t.Fatalf("ReadRange 调用 = %v，期望 [16]", st.readLens)
	}

	// 换一块仍要访问远端：缓存只省重复读，不能把整个文件都当已缓存
	f, err := fsys.OpenFile(t.Context(), "/shared.bin", os.O_RDONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Seek(20, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Read(make([]byte, 1)); err != nil {
		t.Fatal(err)
	}
	if len(st.readLens) != 2 {
		t.Fatalf("ReadRange 调用 = %v，期望换块时再请求一次", st.readLens)
	}
}
