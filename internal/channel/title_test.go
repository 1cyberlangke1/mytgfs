package channel

// 改标题的单测：参数校验、成功调用、标题未变化。

import (
	"context"
	"fmt"
	"testing"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

// fakeTitleAPI 记录改标题请求，并按需返回错误。
type fakeTitleAPI struct {
	hash   int64
	titles []string
	grants []*tg.ChannelsEditAdminRequest
	err    error
}

func (f *fakeTitleAPI) ChannelsGetChannels(context.Context, []tg.InputChannelClass) (tg.MessagesChatsClass, error) {
	return &tg.MessagesChats{Chats: []tg.ChatClass{&tg.Channel{ID: 7, AccessHash: f.hash}}}, nil
}

func (f *fakeTitleAPI) ChannelsEditTitle(_ context.Context, req *tg.ChannelsEditTitleRequest) (tg.UpdatesClass, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.titles = append(f.titles, req.Title)
	return &tg.Updates{}, nil
}

// ContactsResolveUsername 回放 bot 用户，供补权用例断言。
func (f *fakeTitleAPI) ContactsResolveUsername(context.Context, *tg.ContactsResolveUsernameRequest) (*tg.ContactsResolvedPeer, error) {
	return &tg.ContactsResolvedPeer{
		Users: []tg.UserClass{&tg.User{ID: 999, AccessHash: 1234, Bot: true, Username: "mytgfs_2026_bot"}},
	}, nil
}

// ChannelsEditAdmin 记录补权请求，供 GrantBot 用例断言。
func (f *fakeTitleAPI) ChannelsEditAdmin(_ context.Context, req *tg.ChannelsEditAdminRequest) (tg.UpdatesClass, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.grants = append(f.grants, req)
	return &tg.Updates{}, nil
}

// TestSetTitleWritesChannel 正常改标题调用一次并带上解析出的 access_hash。
//
// 输入：accessHash 传 0，标题带首尾空格
// 输出：api 收到一次去空白的标题
// 预期：access_hash 由 channels.getChannels 解析，不依赖调用方传入
func TestSetTitleWritesChannel(t *testing.T) {
	api := &fakeTitleAPI{hash: 42}
	if err := SetTitle(context.Background(), api, 7, 0, "  新名字  "); err != nil {
		t.Fatalf("SetTitle: %v", err)
	}
	if len(api.titles) != 1 || api.titles[0] != "新名字" {
		t.Fatalf("titles = %v，期望 [新名字]", api.titles)
	}
}

// TestSetTitleRejectsEmpty 空标题在本地就被拒绝，不发请求。
func TestSetTitleRejectsEmpty(t *testing.T) {
	api := &fakeTitleAPI{hash: 42}
	if err := SetTitle(context.Background(), api, 7, 42, "   "); err == nil {
		t.Fatal("空标题应当报错")
	}
	if len(api.titles) != 0 {
		t.Fatalf("空标题不该发请求，实际 %v", api.titles)
	}
}

// TestSetTitleToleratesNotModified 标题没变时 Telegram 返回 CHAT_NOT_MODIFIED，视为成功。
func TestSetTitleToleratesNotModified(t *testing.T) {
	api := &fakeTitleAPI{hash: 42, err: tgerr.New(400, "CHAT_NOT_MODIFIED")}
	if err := SetTitle(context.Background(), api, 7, 42, "同名"); err != nil {
		t.Fatalf("CHAT_NOT_MODIFIED 应视为成功，实际 %v", err)
	}
}

// TestIsAdminRequired 只在 CHAT_ADMIN_REQUIRED 时成立，包裹后仍能识别。
func TestIsAdminRequired(t *testing.T) {
	if !IsAdminRequired(tgerr.New(400, "CHAT_ADMIN_REQUIRED")) {
		t.Fatal("CHAT_ADMIN_REQUIRED 应判定为缺权限")
	}
	wrapped := fmt.Errorf("改标题失败: %w", tgerr.New(400, "CHAT_ADMIN_REQUIRED"))
	if !IsAdminRequired(wrapped) {
		t.Fatal("包裹后的错误也应能识别")
	}
	if IsAdminRequired(tgerr.New(400, "CHAT_NOT_MODIFIED")) {
		t.Fatal("CHAT_NOT_MODIFIED 不该当作缺权限")
	}
	if IsAdminRequired(nil) {
		t.Fatal("nil 不该判定为缺权限")
	}
}

// TestBotRightsOnlyWhatWeUse 只授业务实际调用的三项。
//
// 输入：BotRights()
// 输出：PostMessages/EditMessages/ChangeInfo 为 true，删除/置顶为 false
// 预期：权限集合与代码里的调用面一致，token 泄漏时不放大破坏面
func TestBotRightsOnlyWhatWeUse(t *testing.T) {
	r := BotRights()
	if !r.PostMessages || !r.EditMessages || !r.ChangeInfo {
		t.Fatalf("缺少必需权限: %+v", r)
	}
	if r.DeleteMessages || r.PinMessages || r.AddAdmins || r.BanUsers || r.InviteUsers {
		t.Fatalf("不应授予未使用权限: %+v", r)
	}
}

// TestGrantBotSendsRights 补权时下发 BotRights 并在 access_hash 为 0 时先解析。
func TestGrantBotSendsRights(t *testing.T) {
	api := &fakeTitleAPI{hash: 42}
	if err := GrantBotByUsername(context.Background(), api, 7, 0, "mytgfs_2026_bot"); err != nil {
		t.Fatalf("GrantBotByUsername: %v", err)
	}
	if len(api.grants) != 1 {
		t.Fatalf("editAdmin 调用 %d 次，期望 1", len(api.grants))
	}
	got := api.grants[0]
	if got.Channel.(*tg.InputChannel).AccessHash != 42 {
		t.Fatalf("access_hash = %d，期望解析出的 42", got.Channel.(*tg.InputChannel).AccessHash)
	}
	if got.UserID.(*tg.InputUser).UserID != 999 || !got.AdminRights.ChangeInfo {
		t.Fatalf("授权内容不对: %+v", got)
	}
}
