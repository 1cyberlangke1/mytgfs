package sync

import (
	"context"
	"errors"
	"testing"

	"github.com/gotd/td/tg"
)

// fakeHistoryAPI 冒充 user 会话的历史接口。
type fakeHistoryAPI struct {
	historyRes tg.MessagesMessagesClass
	historyErr error
	metaRes    tg.MessagesMessagesClass
	metaErr    error

	historyReqs []*tg.MessagesGetHistoryRequest
	metaReqs    []*tg.ChannelsGetMessagesRequest
}

func (f *fakeHistoryAPI) MessagesGetHistory(_ context.Context, req *tg.MessagesGetHistoryRequest) (tg.MessagesMessagesClass, error) {
	f.historyReqs = append(f.historyReqs, req)
	return f.historyRes, f.historyErr
}

func (f *fakeHistoryAPI) ChannelsGetMessages(_ context.Context, req *tg.ChannelsGetMessagesRequest) (tg.MessagesMessagesClass, error) {
	f.metaReqs = append(f.metaReqs, req)
	return f.metaRes, f.metaErr
}

// docMsg 构造一条带 document 的消息。
func docMsg(id int, caption string, size int64) *tg.Message {
	return &tg.Message{
		ID:      id,
		Message: caption,
		Date:    1700000000,
		Media: &tg.MessageMediaDocument{Document: &tg.Document{
			ID:            int64(9000 + id),
			AccessHash:    int64(5000 + id),
			FileReference: []byte{byte(id)},
			Size:          size,
		}},
	}
}

// TestTelegramFetcherHistory 只保留文件消息，并把 {"ref":id} 展开成完整元信息。
func TestTelegramFetcherHistory(t *testing.T) {
	api := &fakeHistoryAPI{
		historyRes: &tg.MessagesChannelMessages{Messages: []tg.MessageClass{
			docMsg(1, `{"path":"/a.txt"}`, 5),
			docMsg(2, `{"ref":77}`, 6),
			&tg.Message{ID: 3, Message: "纯文本"},                 // 无媒体，跳过
			&tg.Message{ID: 4, Media: &tg.MessageMediaPhoto{}}, // 非 document，跳过
		}},
		metaRes: &tg.MessagesChannelMessages{Messages: []tg.MessageClass{
			&tg.Message{ID: 77, Message: `{"path":"/deep/b.txt"}`},
		}},
	}
	f := TelegramFetcher{API: api, ChannelID: 42, AccessHash: 7}

	msgs, err := f.History(context.Background(), 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("应保留 2 条文件消息，实际 %d", len(msgs))
	}
	if msgs[0].Caption != `{"path":"/a.txt"}` || msgs[0].Size != 5 {
		t.Fatalf("普通 caption 解析错误: %+v", msgs[0])
	}
	if msgs[1].Caption != `{"path":"/deep/b.txt"}` {
		t.Fatalf("ref caption 未展开: %q", msgs[1].Caption)
	}
	if len(api.metaReqs) != 1 || api.metaReqs[0].ID[0].(*tg.InputMessageID).ID != 77 {
		t.Fatalf("元信息请求错误: %+v", api.metaReqs)
	}
}

// TestTelegramFetcherHistorySince 增量时把 minID 传给 getHistory。
func TestTelegramFetcherHistorySince(t *testing.T) {
	api := &fakeHistoryAPI{historyRes: &tg.MessagesChannelMessages{}}
	f := TelegramFetcher{API: api, ChannelID: 42, AccessHash: 7}

	if _, err := f.HistorySince(context.Background(), 31, 20); err != nil {
		t.Fatal(err)
	}
	if len(api.historyReqs) != 1 {
		t.Fatalf("应调用 1 次 getHistory，实际 %d", len(api.historyReqs))
	}
	if got := api.historyReqs[0]; got.MinID != 31 || got.Limit != 20 {
		t.Fatalf("增量参数错误: %+v", got)
	}
}

// TestTelegramFetcherError getHistory 失败时错误要带上下文返回。
func TestTelegramFetcherError(t *testing.T) {
	boom := errors.New("rpc down")
	api := &fakeHistoryAPI{historyErr: boom}
	f := TelegramFetcher{API: api, ChannelID: 42}

	if _, err := f.History(context.Background(), 10); !errors.Is(err, boom) {
		t.Fatalf("错误应可 unwrap，实际 %v", err)
	}
}

// fakeDiffAPI 冒充 bot 会话的频道差异接口。
type fakeDiffAPI struct {
	channelsRes tg.MessagesChatsClass
	channelsErr error
	diffRes     tg.UpdatesChannelDifferenceClass
	diffErr     error
	metaRes     tg.MessagesMessagesClass

	channelCalls int
	diffReqs     []*tg.UpdatesGetChannelDifferenceRequest
	metaReqs     []*tg.ChannelsGetMessagesRequest
}

func (f *fakeDiffAPI) ChannelsGetChannels(_ context.Context, _ []tg.InputChannelClass) (tg.MessagesChatsClass, error) {
	f.channelCalls++
	return f.channelsRes, f.channelsErr
}

func (f *fakeDiffAPI) UpdatesGetChannelDifference(_ context.Context, req *tg.UpdatesGetChannelDifferenceRequest) (tg.UpdatesChannelDifferenceClass, error) {
	f.diffReqs = append(f.diffReqs, req)
	return f.diffRes, f.diffErr
}

func (f *fakeDiffAPI) ChannelsGetMessages(_ context.Context, req *tg.ChannelsGetMessagesRequest) (tg.MessagesMessagesClass, error) {
	f.metaReqs = append(f.metaReqs, req)
	return f.metaRes, nil
}

func newDiffAPI(diff tg.UpdatesChannelDifferenceClass) *fakeDiffAPI {
	return &fakeDiffAPI{
		channelsRes: &tg.MessagesChats{Chats: []tg.ChatClass{&tg.Channel{ID: 42, AccessHash: 777}}},
		diffRes:     diff,
	}
}

// TestChannelDifferenceSlice 切片形态要带回新消息、删除与编辑。
func TestChannelDifferenceSlice(t *testing.T) {
	api := newDiffAPI(&tg.UpdatesChannelDifference{
		Pts:   30,
		Final: false,
		NewMessages: []tg.MessageClass{
			docMsg(20, `{"path":"/new.txt"}`, 3),
			&tg.Message{ID: 21, Message: "no media"},
		},
		OtherUpdates: []tg.UpdateClass{
			&tg.UpdateDeleteChannelMessages{ChannelID: 42, Messages: []int{5, 6}},
			&tg.UpdateEditChannelMessage{Message: docMsg(22, `{"path":"/renamed.txt"}`, 4)},
			&tg.UpdateMessageID{ID: 99}, // 客户端本地事件，应忽略
		},
	})
	d := &TelegramChannelDiffer{API: api, ChannelID: 42}

	diff, err := d.ChannelDifference(context.Background(), 12, 100)
	if err != nil {
		t.Fatal(err)
	}
	if diff.Pts != 30 || diff.Final || diff.TooLong {
		t.Fatalf("基本字段错误: %+v", diff)
	}
	if len(diff.Messages) != 2 {
		t.Fatalf("应为新消息+编辑消息共 2 条，实际 %d", len(diff.Messages))
	}
	if diff.Messages[0].ID != 20 || diff.Messages[1].ID != 22 {
		t.Fatalf("消息顺序/内容错误: %+v", diff.Messages)
	}
	if len(diff.Deleted) != 2 || diff.Deleted[0] != 5 || diff.Deleted[1] != 6 {
		t.Fatalf("删除列表错误: %v", diff.Deleted)
	}
	if req := api.diffReqs[0]; req.Pts != 12 || req.Limit != 100 {
		t.Fatalf("请求参数错误: %+v", req)
	}
	// 频道只解析一次，后续复用 access_hash
	if _, err := d.ChannelDifference(context.Background(), 30, 100); err != nil {
		t.Fatal(err)
	}
	if api.channelCalls != 1 {
		t.Fatalf("频道应只解析 1 次，实际 %d", api.channelCalls)
	}
}

// TestChannelDifferenceEmpty 空差异直接结束。
func TestChannelDifferenceEmpty(t *testing.T) {
	api := newDiffAPI(&tg.UpdatesChannelDifferenceEmpty{Pts: 44, Final: true})
	d := &TelegramChannelDiffer{API: api, ChannelID: 42}

	diff, err := d.ChannelDifference(context.Background(), 44, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !diff.Final || diff.Pts != 44 || len(diff.Messages) != 0 {
		t.Fatalf("空差异解析错误: %+v", diff)
	}
}

// TestChannelDifferenceTooLong 过大差异取 dialog.pts 并带最新消息。
func TestChannelDifferenceTooLong(t *testing.T) {
	api := newDiffAPI(&tg.UpdatesChannelDifferenceTooLong{
		Final:  true,
		Dialog: &tg.Dialog{Pts: 900},
		Messages: []tg.MessageClass{
			docMsg(500, `{"path":"/latest.txt"}`, 7),
		},
	})
	d := &TelegramChannelDiffer{API: api, ChannelID: 42}

	diff, err := d.ChannelDifference(context.Background(), 1, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !diff.TooLong || diff.Pts != 900 {
		t.Fatalf("TooLong 解析错误: %+v", diff)
	}
	if len(diff.Messages) != 1 || diff.Messages[0].ID != 500 {
		t.Fatalf("最新消息缺失: %+v", diff.Messages)
	}
}

// TestChannelDifferenceResolveError 频道解析失败时上报，不静默。
func TestChannelDifferenceResolveError(t *testing.T) {
	boom := errors.New("getChannels failed")
	api := &fakeDiffAPI{channelsErr: boom}
	d := &TelegramChannelDiffer{API: api, ChannelID: 42}

	if _, err := d.ChannelDifference(context.Background(), 1, 100); !errors.Is(err, boom) {
		t.Fatalf("错误应可 unwrap，实际 %v", err)
	}
}
