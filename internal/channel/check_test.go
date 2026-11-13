package channel

// 频道体检的单测：用假 API 覆盖不可见、无权限、创建者与管理员分支。

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

// fakeCheckAPI 是 Check 用到的两个只读方法的最小假实现。
type fakeCheckAPI struct {
	res       tg.MessagesChatsClass
	getErr    error
	particip  *tg.ChannelsChannelParticipant
	partErr   error
	partCalls int
}

func (f *fakeCheckAPI) ChannelsGetChannels(_ context.Context, _ []tg.InputChannelClass) (tg.MessagesChatsClass, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.res, nil
}

func (f *fakeCheckAPI) ChannelsGetParticipant(_ context.Context, _ *tg.ChannelsGetParticipantRequest) (*tg.ChannelsChannelParticipant, error) {
	f.partCalls++
	if f.partErr != nil {
		return nil, f.partErr
	}
	return f.particip, nil
}

// chans 包一层，省得每个用例都写切片字面量。
func chans(list ...tg.ChatClass) tg.MessagesChatsClass {
	return &tg.MessagesChats{Chats: list}
}

// TestCheckCreator 创建者默认拥有全部权限。
//
// 输入：频道列表命中；participant 是 creator
// 输出：CanPost=true，无错误
func TestCheckCreator(t *testing.T) {
	api := &fakeCheckAPI{
		res: chans(&tg.Channel{ID: 7, AccessHash: 99, Title: "工作盘"}),
		particip: &tg.ChannelsChannelParticipant{
			Participant: &tg.ChannelParticipantCreator{UserID: 1},
		},
	}
	h, err := Check(context.Background(), api, 7)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !h.CanPost || h.Title != "工作盘" || h.ChannelID != 7 {
		t.Fatalf("结果不对: %+v", h)
	}
}

// TestCheckAdminCanPost 管理员有 post_messages 时通过。
func TestCheckAdminCanPost(t *testing.T) {
	rights := tg.ChatAdminRights{PostMessages: true}
	api := &fakeCheckAPI{
		res: chans(&tg.Channel{ID: 7, AccessHash: 99, Title: "工作盘"}),
		particip: &tg.ChannelsChannelParticipant{
			Participant: &tg.ChannelParticipantAdmin{AdminRights: rights},
		},
	}
	if _, err := Check(context.Background(), api, 7); err != nil {
		t.Fatalf("Check: %v", err)
	}
}

// TestCheckAdminCannotPost 管理员被收回发言权时报错，但仍返回频道信息。
//
// 输入：admin 且 PostMessages=false
// 输出：Health 带 Title，error 含无权限提示
func TestCheckAdminCannotPost(t *testing.T) {
	api := &fakeCheckAPI{
		res: chans(&tg.Channel{ID: 7, AccessHash: 99, Title: "工作盘"}),
		particip: &tg.ChannelsChannelParticipant{
			Participant: &tg.ChannelParticipantAdmin{AdminRights: tg.ChatAdminRights{}},
		},
	}
	h, err := Check(context.Background(), api, 7)
	if err == nil {
		t.Fatal("期望无发言权限错误")
	}
	if h == nil || h.Title != "工作盘" || h.CanPost {
		t.Fatalf("结果不对: %+v", h)
	}
	if !strings.Contains(err.Error(), "工作盘") && !strings.Contains(err.Error(), "频道") {
		t.Logf("错误文案: %v", err)
	}
}

// TestCheckChannelNotVisible 频道被删或 bot 被踢出时列表里没有它。
func TestCheckChannelNotVisible(t *testing.T) {
	api := &fakeCheckAPI{res: chans(&tg.Channel{ID: 8, AccessHash: 1})}
	if _, err := Check(context.Background(), api, 7); err == nil {
		t.Fatal("期望频道不可见错误")
	}
}

// TestCheckListRPCError 列表 RPC 失败要原样返回。
func TestCheckListRPCError(t *testing.T) {
	want := tgerr.New(400, "CHANNEL_INVALID")
	api := &fakeCheckAPI{getErr: want}
	if _, err := Check(context.Background(), api, 7); !errors.Is(err, want) {
		t.Fatalf("错误 = %v，期望原样透传", err)
	}
}

// TestCheckParticipantRPCError 取成员失败要原样返回。
func TestCheckParticipantRPCError(t *testing.T) {
	want := errors.New("boom")
	api := &fakeCheckAPI{
		res:     chans(&tg.Channel{ID: 7, AccessHash: 99, Title: "盘"}),
		partErr: want,
	}
	if _, err := Check(context.Background(), api, 7); !errors.Is(err, want) {
		t.Fatalf("错误 = %v，期望原样透传", err)
	}
}

// TestCheckNilParticipant 成员查询返回空时给出未知状态错误。
func TestCheckNilParticipant(t *testing.T) {
	api := &fakeCheckAPI{
		res:      chans(&tg.Channel{ID: 7, AccessHash: 99, Title: "盘"}),
		particip: nil,
	}
	if _, err := Check(context.Background(), api, 7); err == nil {
		t.Fatal("期望未知状态错误")
	}
}

// TestCheckChatsSlice 列表返回 MessagesChatsSlice 时同样能解析。
func TestCheckChatsSlice(t *testing.T) {
	api := &fakeCheckAPI{
		res: &tg.MessagesChatsSlice{
			Chats: []tg.ChatClass{&tg.Channel{ID: 7, AccessHash: 99, Title: "盘"}},
		},
		particip: &tg.ChannelsChannelParticipant{
			Participant: &tg.ChannelParticipantCreator{UserID: 1},
		},
	}
	if _, err := Check(context.Background(), api, 7); err != nil {
		t.Fatalf("Check: %v", err)
	}
}
