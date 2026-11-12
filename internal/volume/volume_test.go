package volume

// 建卷流程单测：用假 Telegram API 覆盖成功与各失败分支。

import (
	"context"
	"errors"
	"testing"

	"github.com/gotd/td/tg"

	"mytgfs/internal/channel"
)

// fakeAPI 记录调用参数，并按需返回错误。
type fakeAPI struct {
	createReq *tg.ChannelsCreateChannelRequest
	editReq   *tg.ChannelsEditAdminRequest
	notifyReq *tg.AccountUpdateNotifySettingsRequest

	createErr  error
	resolveErr error
	editErr    error
	notifyErr  error

	users []tg.UserClass
}

func (f *fakeAPI) ChannelsCreateChannel(_ context.Context, req *tg.ChannelsCreateChannelRequest) (tg.UpdatesClass, error) {
	f.createReq = req
	if f.createErr != nil {
		return nil, f.createErr
	}
	return &tg.Updates{Chats: []tg.ChatClass{
		&tg.Channel{ID: 123, AccessHash: 456, Title: req.Title},
	}}, nil
}

func (f *fakeAPI) ContactsResolveUsername(_ context.Context, _ *tg.ContactsResolveUsernameRequest) (*tg.ContactsResolvedPeer, error) {
	if f.resolveErr != nil {
		return nil, f.resolveErr
	}
	return &tg.ContactsResolvedPeer{Users: f.users}, nil
}

func (f *fakeAPI) ChannelsEditAdmin(_ context.Context, req *tg.ChannelsEditAdminRequest) (tg.UpdatesClass, error) {
	f.editReq = req
	if f.editErr != nil {
		return nil, f.editErr
	}
	return &tg.Updates{}, nil
}

func (f *fakeAPI) AccountUpdateNotifySettings(_ context.Context, req *tg.AccountUpdateNotifySettingsRequest) (bool, error) {
	f.notifyReq = req
	if f.notifyErr != nil {
		return false, f.notifyErr
	}
	return true, nil
}

func (f *fakeAPI) MessagesGetDialogs(_ context.Context, _ *tg.MessagesGetDialogsRequest) (tg.MessagesDialogsClass, error) {
	return &tg.MessagesDialogs{}, nil
}

// newFake 返回带一个 bot 用户的假 API。
func newFake() *fakeAPI {
	return &fakeAPI{users: []tg.UserClass{
		&tg.User{ID: 42, AccessHash: 999, Username: "mybot"},
	}}
}

// 确保假实现满足接口。
var _ channel.API = (*fakeAPI)(nil)

func TestCreate(t *testing.T) {
	api := newFake()
	res, err := Create(context.Background(), api, Request{
		Name:        "work",
		BotUsername: "@mybot",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if api.createReq.Title != "work" {
		t.Errorf("title = %q, want %q", api.createReq.Title, "work")
	}
	if !api.createReq.Broadcast {
		t.Error("channel should be broadcast")
	}
	if res.ChannelID != 123 || res.AccessHash != 456 {
		t.Errorf("channel = %d/%d", res.ChannelID, res.AccessHash)
	}
	if res.ChatID != ChatID(123) {
		t.Errorf("chat id = %d", res.ChatID)
	}
	if res.BotID != 42 {
		t.Errorf("bot id = %d", res.BotID)
	}
	if !res.Muted || res.MuteErr != nil {
		t.Errorf("muted = %v err = %v", res.Muted, res.MuteErr)
	}

	// bot 提权：发帖/改/删/置顶都要有
	rights := api.editReq.AdminRights
	if !rights.PostMessages || !rights.EditMessages || !rights.DeleteMessages || !rights.PinMessages {
		t.Errorf("admin rights = %+v", rights)
	}

	// 关通知：永久静音 + 不显示预览
	settings := api.notifyReq.Settings
	if silent, ok := settings.GetSilent(); !ok || !silent {
		t.Error("silent should be true")
	}
	if got, ok := settings.GetMuteUntil(); !ok || got != 2147483647 {
		t.Errorf("mute until = %d (ok=%v), want 2147483647", got, ok)
	}
	if show, ok := settings.GetShowPreviews(); ok && show {
		t.Error("show previews should be false")
	}
}

func TestCreateCustomTitle(t *testing.T) {
	api := newFake()
	if _, err := Create(context.Background(), api, Request{
		Name:        "work",
		Title:       "工作盘",
		BotUsername: "mybot",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if api.createReq.Title != "工作盘" {
		t.Errorf("title = %q", api.createReq.Title)
	}
}

func TestCreateValidatesInput(t *testing.T) {
	api := newFake()
	if _, err := Create(context.Background(), api, Request{BotUsername: "mybot"}); err == nil {
		t.Error("empty name should fail")
	}
	if _, err := Create(context.Background(), api, Request{Name: "work"}); err == nil {
		t.Error("empty bot should fail")
	}
	if api.createReq != nil {
		t.Error("no channel should be created on validation error")
	}
}

func TestCreateChannelError(t *testing.T) {
	api := newFake()
	api.createErr = errors.New("boom")
	if _, err := Create(context.Background(), api, Request{Name: "work", BotUsername: "mybot"}); err == nil {
		t.Fatal("want error")
	}
}

func TestCreateBotNotFound(t *testing.T) {
	api := newFake()
	api.users = nil
	res, err := Create(context.Background(), api, Request{Name: "work", BotUsername: "mybot"})
	if err == nil {
		t.Fatal("want error")
	}
	// 频道已经建出来了，调用方要能拿到 id 去清理
	if res.ChannelID != 123 {
		t.Errorf("partial channel id = %d", res.ChannelID)
	}
}

func TestCreateEditAdminError(t *testing.T) {
	api := newFake()
	api.editErr = errors.New("no rights")
	if _, err := Create(context.Background(), api, Request{Name: "work", BotUsername: "mybot"}); err == nil {
		t.Fatal("want error")
	}
}

func TestCreateMuteFailureIsNotFatal(t *testing.T) {
	api := newFake()
	api.notifyErr = errors.New("flood")
	res, err := Create(context.Background(), api, Request{Name: "work", BotUsername: "mybot"})
	if err != nil {
		t.Fatalf("mute failure should not fail creation: %v", err)
	}
	if res.Muted {
		t.Error("muted should be false")
	}
	if res.MuteErr == nil {
		t.Error("mute error should be kept")
	}
}

func TestChatID(t *testing.T) {
	if got := ChatID(1234567890); got != -1001234567890 {
		t.Errorf("ChatID = %d", got)
	}
}
