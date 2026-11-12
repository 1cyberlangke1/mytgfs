package channel

// 解析与删除频道的单测：用假 API 覆盖命中、未命中与 RPC 失败。

import (
	"context"
	"errors"
	"testing"

	"github.com/gotd/td/tg"
)

// fakeDeleteAPI 记录删除调用，并按需返回错误。
type fakeDeleteAPI struct {
	chats    []tg.ChatClass
	getErr   error
	deleteID int64
	delErr   error
}

func (f *fakeDeleteAPI) ChannelsGetChannels(_ context.Context, _ []tg.InputChannelClass) (tg.MessagesChatsClass, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return &tg.MessagesChats{Chats: f.chats}, nil
}

func (f *fakeDeleteAPI) ChannelsDeleteChannel(_ context.Context, c tg.InputChannelClass) (tg.UpdatesClass, error) {
	if in, ok := c.(*tg.InputChannel); ok {
		f.deleteID = in.ChannelID
	}
	if f.delErr != nil {
		return nil, f.delErr
	}
	return &tg.Updates{}, nil
}

func TestResolve(t *testing.T) {
	api := &fakeDeleteAPI{chats: []tg.ChatClass{
		&tg.Channel{ID: 7, AccessHash: 99, Title: "工作盘"},
	}}
	res, err := Resolve(context.Background(), api, 7)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.AccessHash != 99 || res.Title != "工作盘" {
		t.Errorf("res = %+v", res)
	}
}

func TestResolveNotFound(t *testing.T) {
	api := &fakeDeleteAPI{}
	if _, err := Resolve(context.Background(), api, 7); err == nil {
		t.Fatal("want error")
	}
}

func TestResolveRPCError(t *testing.T) {
	api := &fakeDeleteAPI{getErr: errors.New("boom")}
	if _, err := Resolve(context.Background(), api, 7); err == nil {
		t.Fatal("want error")
	}
}

func TestDelete(t *testing.T) {
	api := &fakeDeleteAPI{}
	if err := Delete(context.Background(), api, &Result{ChannelID: 7, AccessHash: 99}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if api.deleteID != 7 {
		t.Errorf("delete id = %d", api.deleteID)
	}
}

func TestDeleteValidatesInput(t *testing.T) {
	api := &fakeDeleteAPI{}
	if err := Delete(context.Background(), api, nil); err == nil {
		t.Fatal("nil result should fail")
	}
	if err := Delete(context.Background(), api, &Result{}); err == nil {
		t.Fatal("zero channel id should fail")
	}
}

func TestDeleteRPCError(t *testing.T) {
	api := &fakeDeleteAPI{delErr: errors.New("no rights")}
	if err := Delete(context.Background(), api, &Result{ChannelID: 7}); err == nil {
		t.Fatal("want error")
	}
}
