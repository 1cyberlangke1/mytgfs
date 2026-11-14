package channel

// 用 user 会话列出「当前 user 能管理」的频道。

import (
	"context"

	"github.com/gotd/td/tg"
)

// Item 是一个频道摘要。
//
// 字段说明：ID 是频道原始 id；ChatID 是 Bot API 风格的 -100... 形式，
// 面板与配置都用它，避免前端再算一次。
type Item struct {
	ID         int64  `json:"id"`
	AccessHash int64  `json:"-"`
	ChatID     int64  `json:"chat_id"`
	Title      string `json:"title"`
	Username   string `json:"username"`
	Creator    bool   `json:"creator"`
	Admin      bool   `json:"admin"`
}

// List 列出当前 user 能管理的频道/超级群。
//
// 输入：ctx；api user 会话的 Telegram API
// 输出：频道列表
// 预期：creator 与拥有管理权限的管理员频道都会返回；普通成员频道不返回
func List(ctx context.Context, api API) ([]Item, error) {
	// 主文件夹与归档文件夹各拉一次：归档里的私有频道同样是可用存储卷，
	// 只查主文件夹会让它们凭空消失。
	var chats []tg.ChatClass
	for _, folder := range []int{0, 1} {
		req := &tg.MessagesGetDialogsRequest{
			OffsetPeer: &tg.InputPeerEmpty{},
			Limit:      200,
		}
		if folder > 0 {
			req.SetFolderID(folder)
		}
		res, err := api.MessagesGetDialogs(ctx, req)
		if err != nil {
			// 没有归档文件夹的账号调用会报错，忽略即可，不影响主文件夹结果
			if folder > 0 {
				continue
			}
			return nil, err
		}
		switch m := res.(type) {
		case *tg.MessagesDialogs:
			chats = append(chats, m.Chats...)
		case *tg.MessagesDialogsSlice:
			chats = append(chats, m.Chats...)
		}
	}

	seen := make(map[int64]struct{}, len(chats))
	out := make([]Item, 0, len(chats))
	for _, c := range chats {
		ch, ok := c.(*tg.Channel)
		if !ok {
			continue
		}
		if _, dup := seen[ch.ID]; dup {
			continue
		}
		rights, hasRights := ch.GetAdminRights()
		if !ch.Creator && !(hasRights && canManage(rights)) {
			continue
		}
		seen[ch.ID] = struct{}{}
		out = append(out, Item{
			ID:         ch.ID,
			AccessHash: ch.AccessHash,
			ChatID:     -1000000000000 - ch.ID,
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
