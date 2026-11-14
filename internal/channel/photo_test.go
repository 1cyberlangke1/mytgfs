package channel

// 频道头像写入的单测：假 API 记录上传分片与 editPhoto 参数。

import (
	"context"
	"errors"
	"testing"

	"github.com/gotd/td/tg"
)

// fakePhotoAPI 只实现头像相关方法，其余调用直接 panic。
type fakePhotoAPI struct {
	uploadReq *tg.UploadSaveFilePartRequest
	photoReq  *tg.ChannelsEditPhotoRequest
	chatReq   []tg.InputChannelClass

	chats     []tg.ChatClass
	getErr    error
	uploadOK  bool
	uploadErr error
	photoErr  error
}

func (f *fakePhotoAPI) ChannelsGetChannels(_ context.Context, req []tg.InputChannelClass) (tg.MessagesChatsClass, error) {
	f.chatReq = req
	if f.getErr != nil {
		return nil, f.getErr
	}
	return &tg.MessagesChats{Chats: f.chats}, nil
}

func (f *fakePhotoAPI) UploadSaveFilePart(_ context.Context, req *tg.UploadSaveFilePartRequest) (bool, error) {
	f.uploadReq = req
	return f.uploadOK, f.uploadErr
}

func (f *fakePhotoAPI) ChannelsEditPhoto(_ context.Context, req *tg.ChannelsEditPhotoRequest) (tg.UpdatesClass, error) {
	f.photoReq = req
	if f.photoErr != nil {
		return nil, f.photoErr
	}
	return &tg.Updates{}, nil
}

func (f *fakePhotoAPI) ChannelsCreateChannel(context.Context, *tg.ChannelsCreateChannelRequest) (tg.UpdatesClass, error) {
	panic("unused")
}

func (f *fakePhotoAPI) ContactsResolveUsername(context.Context, *tg.ContactsResolveUsernameRequest) (*tg.ContactsResolvedPeer, error) {
	panic("unused")
}

func (f *fakePhotoAPI) ChannelsEditAdmin(context.Context, *tg.ChannelsEditAdminRequest) (tg.UpdatesClass, error) {
	panic("unused")
}

func (f *fakePhotoAPI) AccountUpdateNotifySettings(context.Context, *tg.AccountUpdateNotifySettingsRequest) (bool, error) {
	panic("unused")
}

func (f *fakePhotoAPI) MessagesGetDialogs(context.Context, *tg.MessagesGetDialogsRequest) (tg.MessagesDialogsClass, error) {
	panic("unused")
}

var _ API = (*fakePhotoAPI)(nil)

// TestSetPhotoUploadsOnePart 给定 access_hash 时只发 0 号分片并调 editPhoto。
func TestSetPhotoUploadsOnePart(t *testing.T) {
	api := &fakePhotoAPI{uploadOK: true}
	data := []byte("jpeg-bytes")
	if err := SetPhoto(context.Background(), api, 123, 456, data); err != nil {
		t.Fatalf("SetPhoto: %v", err)
	}
	if api.chatReq != nil {
		t.Error("已知 access_hash 不该再查频道")
	}
	if api.uploadReq == nil || api.uploadReq.FilePart != 0 {
		t.Fatalf("分片请求不符: %+v", api.uploadReq)
	}
	if string(api.uploadReq.Bytes) != string(data) {
		t.Error("上传内容被改动")
	}
	ch, ok := api.photoReq.Channel.(*tg.InputChannel)
	if !ok || ch.ChannelID != 123 || ch.AccessHash != 456 {
		t.Errorf("频道参数不符: %+v", api.photoReq.Channel)
	}
	up, ok := api.photoReq.Photo.(*tg.InputChatUploadedPhoto)
	if !ok {
		t.Fatalf("photo = %T", api.photoReq.Photo)
	}
	f, ok := up.File.(*tg.InputFile)
	if !ok || f.ID != api.uploadReq.FileID || f.Parts != 1 {
		t.Errorf("文件引用不符: %+v", up.File)
	}
}

// TestSetPhotoResolvesAccessHash access_hash=0 时先解析再写头像。
func TestSetPhotoResolvesAccessHash(t *testing.T) {
	api := &fakePhotoAPI{
		uploadOK: true,
		chats:    []tg.ChatClass{&tg.Channel{ID: 123, AccessHash: 789}},
	}
	if err := SetPhoto(context.Background(), api, 123, 0, []byte("x")); err != nil {
		t.Fatalf("SetPhoto: %v", err)
	}
	if len(api.chatReq) != 1 {
		t.Fatalf("没有解析频道: %+v", api.chatReq)
	}
	ch, ok := api.photoReq.Channel.(*tg.InputChannel)
	if !ok || ch.AccessHash != 789 {
		t.Errorf("未使用解析出的 access_hash: %+v", api.photoReq.Channel)
	}
}

// TestSetPhotoRejectsBadInput 空数据与超大块都在发 RPC 前失败。
func TestSetPhotoRejectsBadInput(t *testing.T) {
	api := &fakePhotoAPI{uploadOK: true}
	if err := SetPhoto(context.Background(), api, 1, 1, nil); err == nil {
		t.Error("空数据应该失败")
	}
	big := make([]byte, maxPhotoBytes+1)
	if err := SetPhoto(context.Background(), api, 1, 1, big); err == nil {
		t.Error("超过单块上限应该失败")
	}
	if api.uploadReq != nil {
		t.Error("非法输入不该发上传")
	}
}

// TestSetPhotoUploadRejected 服务端返回 false 时不再 editPhoto。
func TestSetPhotoUploadRejected(t *testing.T) {
	api := &fakePhotoAPI{uploadOK: false}
	if err := SetPhoto(context.Background(), api, 1, 1, []byte("x")); err == nil {
		t.Fatal("上传被拒应该失败")
	}
	if api.photoReq != nil {
		t.Error("上传失败后不该改头像")
	}
}

// TestSetPhotoChannelMissing 解析不到频道时报「不可见」。
func TestSetPhotoChannelMissing(t *testing.T) {
	api := &fakePhotoAPI{uploadOK: true}
	if err := SetPhoto(context.Background(), api, 9, 0, []byte("x")); err == nil {
		t.Fatal("频道不可见应该失败")
	}
}

// TestSetPhotoEditError 头像写入失败要把原因透出来。
func TestSetPhotoEditError(t *testing.T) {
	api := &fakePhotoAPI{uploadOK: true, photoErr: errors.New("CHAT_ADMIN_REQUIRED")}
	if err := SetPhoto(context.Background(), api, 1, 1, []byte("x")); err == nil {
		t.Fatal("editPhoto 失败应该返回错误")
	}
}
