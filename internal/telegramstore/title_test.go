package telegramstore

// 频道标题读取：面板用它作为卷的显示名。

import "testing"

// Title 返回频道标题；缓存失效后重新回源，拿到外部改过的名字。
func TestTitleCachesUntilInvalidated(t *testing.T) {
	api := &fakeAPI{channelTitle: "我的盘"}
	st := New(api, 42)

	got, err := st.Title(t.Context())
	if err != nil {
		t.Fatalf("Title 出错: %v", err)
	}
	if got != "我的盘" {
		t.Fatalf("Title = %q, 期望 我的盘", got)
	}
	calls := api.channelCalls

	// 未失效：走缓存，不再打 Telegram
	if _, err := st.Title(t.Context()); err != nil {
		t.Fatalf("二次 Title 出错: %v", err)
	}
	if api.channelCalls != calls {
		t.Fatalf("二次 Title 打了 %d 次接口, 期望复用缓存", api.channelCalls-calls)
	}

	// 外部改了频道标题：失效后必须拿到新值
	api.channelTitle = "改过的名字"
	st.InvalidateCover()
	got, err = st.Title(t.Context())
	if err != nil {
		t.Fatalf("失效后 Title 出错: %v", err)
	}
	if got != "改过的名字" {
		t.Fatalf("失效后 Title = %q, 期望 改过的名字", got)
	}
}
