package volume

// 建卷流程单测：用假 Telegram API 覆盖成功与各失败分支。

import (
	"bytes"
	"context"
	"errors"
	"image/jpeg"
	"testing"

	"github.com/gotd/td/tg"

	"mytgfs/internal/channel"
)

// fakeAPI 记录调用参数，并按需返回错误。
type fakeAPI struct {
	createReq *tg.ChannelsCreateChannelRequest
	editReq   *tg.ChannelsEditAdminRequest
	notifyReq *tg.AccountUpdateNotifySettingsRequest
	uploadReq *tg.UploadSaveFilePartRequest
	photoReq  *tg.ChannelsEditPhotoRequest

	createErr  error
	resolveErr error
	editErr    error
	notifyErr  error
	uploadErr  error
	photoErr   error

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

func (f *fakeAPI) ChannelsGetChannels(_ context.Context, _ []tg.InputChannelClass) (tg.MessagesChatsClass, error) {
	return &tg.MessagesChats{Chats: []tg.ChatClass{
		&tg.Channel{ID: 123, AccessHash: 456},
	}}, nil
}

func (f *fakeAPI) ChannelsEditPhoto(_ context.Context, req *tg.ChannelsEditPhotoRequest) (tg.UpdatesClass, error) {
	f.photoReq = req
	if f.photoErr != nil {
		return nil, f.photoErr
	}
	return &tg.Updates{}, nil
}

func (f *fakeAPI) UploadSaveFilePart(_ context.Context, req *tg.UploadSaveFilePartRequest) (bool, error) {
	f.uploadReq = req
	if f.uploadErr != nil {
		return false, f.uploadErr
	}
	return true, nil
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

// TestCreateSetsDefaultAvatar 新卷默认头像使用项目图标。
//
// 输入：建卷成功路径
// 输出：上传分片 0 一次，editPhoto 一次
// 预期：上传内容为 512×512 JPEG，频道用刚解析出的 access_hash
func TestCreateSetsDefaultAvatar(t *testing.T) {
	api := newFake()
	res, err := Create(context.Background(), api, Request{Name: "work", BotUsername: "mybot"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if res.AvatarErr != nil {
		t.Fatalf("默认头像失败: %v", res.AvatarErr)
	}
	if api.uploadReq == nil || api.photoReq == nil {
		t.Fatal("没有调用上传或 editPhoto")
	}
	if api.uploadReq.FilePart != 0 {
		t.Errorf("分片号 = %d，期望 0", api.uploadReq.FilePart)
	}
	if len(api.uploadReq.Bytes) == 0 {
		t.Error("上传内容为空")
	}
	if w, h := jpegSize(t, api.uploadReq.Bytes); w != 512 || h != 512 {
		t.Errorf("默认头像尺寸 = %d×%d，期望 512×512", w, h)
	}
	ch, ok := api.photoReq.Channel.(*tg.InputChannel)
	if !ok || ch.ChannelID != 123 || ch.AccessHash != 456 {
		t.Errorf("editPhoto 频道 = %+v", api.photoReq.Channel)
	}
	up, ok := api.photoReq.Photo.(*tg.InputChatUploadedPhoto)
	if !ok {
		t.Fatalf("photo = %T", api.photoReq.Photo)
	}
	f, ok := up.File.(*tg.InputFile)
	if !ok || f.Parts != 1 || f.ID != api.uploadReq.FileID {
		t.Errorf("上传文件引用不符: %+v", up.File)
	}
}

// TestCreateAvatarFailureIsNotFatal 头像设失败不影响卷可用。
func TestCreateAvatarFailureIsNotFatal(t *testing.T) {
	api := newFake()
	api.photoErr = errors.New("no rights")
	res, err := Create(context.Background(), api, Request{Name: "work", BotUsername: "mybot"})
	if err != nil {
		t.Fatalf("头像失败不该中断建卷: %v", err)
	}
	if res.AvatarErr == nil {
		t.Error("AvatarErr 应该保留失败原因")
	}
}

// jpegSize 解出 JPEG 的宽高。
func jpegSize(t *testing.T, data []byte) (int, int) {
	t.Helper()
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("不是 JPEG: %v", err)
	}
	b := img.Bounds()
	return b.Dx(), b.Dy()
}

func TestChatID(t *testing.T) {
	if got := ChatID(1234567890); got != -1001234567890 {
		t.Errorf("ChatID = %d", got)
	}
}
