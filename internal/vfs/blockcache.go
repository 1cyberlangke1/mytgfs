package vfs

// 读块缓存：多个 File 句柄共享同一份远端块。
//
// 输入：块键（节点消息号 + 块下标）与块数据
// 输出：命中时直接返回字节切片
// 预期：媒体按窗口反复读取时不重复下载同一块；容量按字节封顶，先进先出淘汰

import (
	"container/list"
	"sync"
)

// blockCacheBytes 是所有卷共享的读块缓存上限。
// 取 64MiB：够放一个 1MiB 块的 64 份，媒体拖动与多路窗口都在此之内。
const blockCacheBytes = 64 << 20

// blockKey 唯一定位一个读块。
//
// id 用节点消息号：覆盖写会产生新消息号，因此同一 id 的内容不会变；
// index 是「偏移 / 块大小」；length 是这一块的字节数，
// 块大小改过之后长度不同，不会把旧块当成同一块。
type blockKey struct {
	id     int
	index  int64
	length int
}

type blockEntry struct {
	key blockKey
	buf []byte
}

// blockCache 是按字节数封顶的 LRU。
type blockCache struct {
	mu    sync.Mutex
	max   int
	used  int
	ll    *list.List
	items map[blockKey]*list.Element
}

// newBlockCache 创建缓存。
//
// 输入：max 字节上限，<=0 时用 blockCacheBytes
// 输出：可并发使用的 *blockCache
func newBlockCache(max int) *blockCache {
	if max <= 0 {
		max = blockCacheBytes
	}
	return &blockCache{
		max:   max,
		ll:    list.New(),
		items: make(map[blockKey]*list.Element),
	}
}

// Get 取一块。
//
// 输入：key
// 输出：块数据与是否命中
// 预期：命中就返回缓存块；块长比请求的短也算命中，
// 读取方会在需要更多字节时自己重新取块
func (c *blockCache) Get(key blockKey) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[key]
	if !ok {
		return nil, false
	}
	c.ll.MoveToFront(el)
	return el.Value.(*blockEntry).buf, true
}

// Put 存一块，并淘汰超出上限的最旧块。
//
// 输入：key；buf 块数据（调用方之后不应再修改）
// 输出：无
func (c *blockCache) Put(key blockKey, buf []byte) {
	if len(buf) == 0 || len(buf) > c.max {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		e := el.Value.(*blockEntry)
		c.used -= len(e.buf)
		e.buf = buf
		c.used += len(buf)
		c.ll.MoveToFront(el)
	} else {
		c.items[key] = c.ll.PushFront(&blockEntry{key: key, buf: buf})
		c.used += len(buf)
	}
	for c.used > c.max {
		el := c.ll.Back()
		if el == nil {
			break
		}
		e := el.Value.(*blockEntry)
		c.ll.Remove(el)
		delete(c.items, e.key)
		c.used -= len(e.buf)
	}
}

// Drop 丢掉一个节点的全部块。
//
// 输入：id 节点消息号
// 输出：无
// 预期：删除文件后不必等淘汰就把内存还回来
func (c *blockCache) Drop(id int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for key, el := range c.items {
		if key.id != id {
			continue
		}
		e := el.Value.(*blockEntry)
		c.ll.Remove(el)
		delete(c.items, key)
		c.used -= len(e.buf)
	}
}

// Len 返回当前缓存的块数与占用字节数，仅用于测试与诊断。
func (c *blockCache) Len() (blocks int, bytes int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.items), c.used
}
