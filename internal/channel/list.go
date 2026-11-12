package channel

// 用 user 会话列出「当前 user 能管理」的频道。

import (
	"context"
	"fmt"

	"github.com/gotd/td/tg"
)

// Item 是一个频道摘要。
type Item struct {
	ID         int64
	AccessHash int64
	Title      string
	Username   string
	Creator    bool
	Admin      bool
}

// List 列出当前 user 能管理的频道/超级群。
//
// 输入：ctx；api user 会话的 Telegram API
// 输出：频道列表
// 预期：creator 与拥有管理权限的管理员频道都会返回；普通成员频道不返回
func List(ctx context.Context, api API) ([]Item, error) {
	res, err := api.MessagesGetDialogs(ctx, &tg.MessagesGetDialogsRequest{
		OffsetPeer: &tg.InputPeerEmpty{},
		Limit:      200,
	})
	if err != nil {
		return nil, fmt.Errorf("getDialogs: %w", err)
	}

	var chats []tg.ChatClass
	switch m := res.(type) {
	case *tg.MessagesDialogs:
		chats = m.Chats
	case *tg.MessagesDialogsSlice:
		chats = m.Chats
	}

	out := make([]Item, 0, len(chats))
	for _, c := range chats {
		ch, ok := c.(*tg.Channel)
		if !ok {
			continue
		}
		rights, hasRights := ch.GetAdminRights()
		if !ch.Creator && !(hasRights && canManage(rights)) {
			continue
		}
		out = append(out, Item{
			ID:         ch.ID,
			AccessHash: ch.AccessHash,
			Title:      ch.Title,
			Username:   ch.Username,
			Creator:    ch.Creator,
			Admin:      hasRights,
		})
	}
	return out, nil
}

// canManage 判断管理员权限里是否包含管理内容所需的能力。
//
// 输入：管理员权限
// 输出：true 表示能发帖/编辑/删除/邀请，足以作为文件存储频道
// 预期：纯只读或仅查看统计的管理员返回 false
func canManage(r tg.ChatAdminRights) bool {
	return r.ChangeInfo || r.PostMessages || r.EditMessages ||
		r.DeleteMessages || r.InviteUsers || r.PinMessages || r.AddAdmins
}
