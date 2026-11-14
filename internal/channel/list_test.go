package channel

// 频道列表单测：固定「主文件夹 + 归档文件夹都要查」「创建者与管理员都要列出」。
// 这两个条件任何一条丢了，用户的私有频道就会从「添加已有频道」里消失。

import (
	"context"
	"testing"

	"github.com/gotd/td/tg"
)

// fakeListAPI 只为 MessagesGetDialogs 返回数据，其余方法不会被调用。
type fakeListAPI struct {
	main     []tg.ChatClass
	archived []tg.ChatClass
	archErr  error
	calls    []int
}

func (f *fakeListAPI) MessagesGetDialogs(_ context.Context, req *tg.MessagesGetDialogsRequest) (tg.MessagesDialogsClass, error) {
	folder, _ := req.GetFolderID()
	f.calls = append(f.calls, folder)
	if folder == 1 {
		if f.archErr != nil {
			return nil, f.archErr
		}
		return &tg.MessagesDialogsSlice{Chats: f.archived}, nil
	}
	return &tg.MessagesDialogsSlice{Chats: f.main}, nil
}

func (f *fakeListAPI) ChannelsCreateChannel(context.Context, *tg.ChannelsCreateChannelRequest) (tg.UpdatesClass, error) {
	panic("unused")
}
func (f *fakeListAPI) ContactsResolveUsername(context.Context, *tg.ContactsResolveUsernameRequest) (*tg.ContactsResolvedPeer, error) {
	panic("unused")
}
func (f *fakeListAPI) ChannelsEditAdmin(context.Context, *tg.ChannelsEditAdminRequest) (tg.UpdatesClass, error) {
	panic("unused")
}
func (f *fakeListAPI) AccountUpdateNotifySettings(context.Context, *tg.AccountUpdateNotifySettingsRequest) (bool, error) {
	panic("unused")
}

// mkChannel 造一个频道；admin 为 false 时表示普通成员频道。
func mkChannel(id int64, creator, admin bool) *tg.Channel {
	ch := &tg.Channel{ID: id, Title: "ch", AccessHash: id * 7, Creator: creator}
	if admin {
		ch.SetAdminRights(tg.ChatAdminRights{PostMessages: true})
	}
	return ch
}

// TestListIncludesArchivedAndAdmin 固定列表口径。
//
// 输入：主文件夹有创建者频道+普通频道；归档文件夹有管理员频道
// 输出：创建者与管理员各一条；普通频道被过滤；两次请求文件夹 0 和 1
// 预期：归档频道不会因为不在主文件夹而消失
func TestListIncludesArchivedAndAdmin(t *testing.T) {
	api := &fakeListAPI{
		main:     []tg.ChatClass{mkChannel(1, true, false), mkChannel(2, false, false)},
		archived: []tg.ChatClass{mkChannel(3, false, true)},
	}
	items, err := List(context.Background(), api)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("items=%d, 期望 2（创建者+管理员）", len(items))
	}
	byID := map[int64]Item{}
	for _, it := range items {
		byID[it.ID] = it
	}
	if !byID[1].Creator {
		t.Fatalf("频道 1 应为创建者")
	}
	if !byID[3].Admin || byID[3].Creator {
		t.Fatalf("频道 3 应为管理员而非创建者: %+v", byID[3])
	}
	if len(api.calls) != 2 || api.calls[0] != 0 || api.calls[1] != 1 {
		t.Fatalf("folder 调用序列 = %v, 期望 [0 1]", api.calls)
	}
}

// TestListArchivedErrorIgnored 归档请求失败不应影响主文件夹结果。
//
// 输入：主文件夹有一个创建者频道；归档请求报错
// 输出：仍返回主文件夹那一条，error 为 nil
// 预期：没有归档文件夹的账号也能正常列频道
func TestListArchivedErrorIgnored(t *testing.T) {
	api := &fakeListAPI{
		main:    []tg.ChatClass{mkChannel(10, true, false)},
		archErr: context.DeadlineExceeded,
	}
	items, err := List(context.Background(), api)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 1 || items[0].ID != 10 {
		t.Fatalf("items=%+v", items)
	}
}

// TestListDedupes 同一频道出现在两个文件夹时只留一条。
//
// 输入：主文件夹与归档都返回频道 5
// 输出：只有一条
// 预期：不会在下拉里出现重复项
func TestListDedupes(t *testing.T) {
	api := &fakeListAPI{
		main:     []tg.ChatClass{mkChannel(5, true, false)},
		archived: []tg.ChatClass{mkChannel(5, true, false)},
	}
	items, err := List(context.Background(), api)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items=%d, 期望 1", len(items))
	}
}
