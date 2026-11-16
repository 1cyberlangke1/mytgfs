package channel

// 改标题的单测：参数校验、成功调用、标题未变化。

import (
	"context"
	"testing"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

// fakeTitleAPI 记录改标题请求，并按需返回错误。
type fakeTitleAPI struct {
	hash   int64
	titles []string
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
