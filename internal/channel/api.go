package channel

// 频道操作依赖的最小 Telegram 接口。
//
// 方法签名与 tg.Client 完全一致，用接口是为了让单测能替换成假实现，
// 不必连真实的 Telegram。

import (
	"context"

	"github.com/gotd/td/tg"
)

// API 是本包用到的 Telegram 方法集合。
//
// 输入/输出：与 tg.Client 的同名方法一致
// 预期：tg.Client 天然满足；测试用假实现覆盖成功与各失败分支
type API interface {
	ChannelsCreateChannel(ctx context.Context, req *tg.ChannelsCreateChannelRequest) (tg.UpdatesClass, error)
	ContactsResolveUsername(ctx context.Context, req *tg.ContactsResolveUsernameRequest) (*tg.ContactsResolvedPeer, error)
	ChannelsEditAdmin(ctx context.Context, req *tg.ChannelsEditAdminRequest) (tg.UpdatesClass, error)
	AccountUpdateNotifySettings(ctx context.Context, req *tg.AccountUpdateNotifySettingsRequest) (bool, error)
	MessagesGetDialogs(ctx context.Context, req *tg.MessagesGetDialogsRequest) (tg.MessagesDialogsClass, error)
}
