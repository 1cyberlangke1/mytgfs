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

// TestSetTitleUsesBotSession bot 会话改标题：去空白后下发，并清掉频道缓存。
//
// 输入：标题带首尾空格
// 输出：api 收到一次去空白的 channels.editTitle
// 预期：不需要 user 会话，建卷时已授 ChangeInfo
func TestSetTitleUsesBotSession(t *testing.T) {
	api := &fakeAPI{channelTitle: "旧名字"}
	st := New(api, 42)
	if err := st.SetTitle(t.Context(), "  新名字  "); err != nil {
		t.Fatalf("SetTitle: %v", err)
	}
	if len(api.editTitles) != 1 {
		t.Fatalf("editTitle 调用 %d 次，期望 1", len(api.editTitles))
	}
	if got := api.editTitles[0].Title; got != "新名字" {
		t.Fatalf("标题 = %q，期望 新名字", got)
	}
}

// TestSetTitleRejectsEmpty 空标题在本地被拒，不发请求。
func TestSetTitleRejectsEmpty(t *testing.T) {
	api := &fakeAPI{channelTitle: "旧名字"}
	st := New(api, 42)
	if err := st.SetTitle(t.Context(), "   "); err == nil {
		t.Fatal("空标题应当报错")
	}
	if len(api.editTitles) != 0 {
		t.Fatalf("空标题不该发请求，实际 %d 次", len(api.editTitles))
	}
}

// TestBotUsernameReadsSelf BotUsername 读 bot 自身用户名（补授权时用）。
func TestBotUsernameReadsSelf(t *testing.T) {
	st := New(&fakeAPI{}, 42)
	name, err := st.BotUsername(t.Context())
	if err != nil {
		t.Fatalf("BotUsername: %v", err)
	}
	if name != "mytgfs_2026_bot" {
		t.Fatalf("BotUsername = %q，期望 mytgfs_2026_bot", name)
	}
}
