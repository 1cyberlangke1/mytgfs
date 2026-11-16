package telegramstore

// 频道 access_hash 缓存：命中不发 RPC、未命中写回、失效清掉、标题仍回源。

import (
	"context"
	"testing"
)

// fakeChannelCache 记录读写删，供用例断言。
type fakeChannelCache struct {
	hash    int64
	ok      bool
	saved   []int64
	dropped []int64
}

func (f *fakeChannelCache) LoadAccessHash(context.Context, int64) (int64, bool, error) {
	return f.hash, f.ok, nil
}

func (f *fakeChannelCache) SaveAccessHash(_ context.Context, _ int64, accessHash int64) error {
	f.saved = append(f.saved, accessHash)
	return nil
}

func (f *fakeChannelCache) DropAccessHash(_ context.Context, chatID int64) error {
	f.dropped = append(f.dropped, chatID)
	return nil
}

// TestResolveUsesCachedAccessHash 命中缓存时零 RPC。
func TestResolveUsesCachedAccessHash(t *testing.T) {
	api := &fakeAPI{}
	st := New(api, 42)
	st.SetChannelCache(&fakeChannelCache{hash: 555, ok: true})

	ch, err := st.resolve(t.Context())
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if ch.AccessHash != 555 {
		t.Fatalf("access_hash = %d，期望 555", ch.AccessHash)
	}
	if api.channelCalls != 0 {
		t.Fatalf("命中缓存仍打了 %d 次 getChannels", api.channelCalls)
	}
}

// TestResolveFillsCacheOnMiss 未命中时回源一次并写回缓存。
func TestResolveFillsCacheOnMiss(t *testing.T) {
	api := &fakeAPI{channelTitle: "我的盘"}
	st := New(api, 42)
	cache := &fakeChannelCache{}
	st.SetChannelCache(cache)

	if _, err := st.resolve(t.Context()); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if api.channelCalls != 1 {
		t.Fatalf("getChannels 调用 %d 次，期望 1", api.channelCalls)
	}
	if len(cache.saved) != 1 || cache.saved[0] == 0 {
		t.Fatalf("未写回 access_hash: %v", cache.saved)
	}
}

// TestDropChannelClearsCache access_hash 失效时连本地缓存一起清。
func TestDropChannelClearsCache(t *testing.T) {
	st := New(&fakeAPI{}, 42)
	cache := &fakeChannelCache{hash: 555, ok: true}
	st.SetChannelCache(cache)

	st.dropChannel()
	if len(cache.dropped) != 1 || cache.dropped[0] != 42 {
		t.Fatalf("未清缓存: %v", cache.dropped)
	}
}

// TestTitleRefetchesWhenOnlyHashCached 只有 access_hash 时，读标题回源一次。
//
// 输入：缓存命中但内存里没有标题
// 输出：标题来自 RPC，且只打一次 getChannels
// 预期：缓存不参与标题，避免显示过期名字
func TestTitleRefetchesWhenOnlyHashCached(t *testing.T) {
	api := &fakeAPI{channelTitle: "新名字"}
	st := New(api, 42)
	st.SetChannelCache(&fakeChannelCache{hash: 555, ok: true})

	got, err := st.Title(t.Context())
	if err != nil {
		t.Fatalf("Title: %v", err)
	}
	if got != "新名字" {
		t.Fatalf("Title = %q，期望 新名字", got)
	}
	if api.channelCalls != 1 {
		t.Fatalf("getChannels 调用 %d 次，期望 1", api.channelCalls)
	}
}
