package sync

// Telegram 侧的差异拉取：只取消息号，节点语义由本地索引解释。

import (
	"context"
	"fmt"
	"sync"

	"github.com/gotd/td/tg"

	"mytgfs/internal/i18n"
)

// ChannelDiffAPI 是频道增量同步所需的最小 RPC 子集。
//
// 输入：解析频道、拉差异的参数
// 输出：Telegram 原始返回
// 预期：bot 会话满足；测试注入假实现即可覆盖全部差异分支
type ChannelDiffAPI interface {
	ChannelsGetChannels(ctx context.Context, id []tg.InputChannelClass) (tg.MessagesChatsClass, error)
	UpdatesGetChannelDifference(ctx context.Context, request *tg.UpdatesGetChannelDifferenceRequest) (tg.UpdatesChannelDifferenceClass, error)
}

// TelegramChannelDiffer 用 updates.getChannelDifference 拉频道差异。
//
// 文档：https://core.telegram.org/method/updates.getChannelDifference
// 该方法 both users and bots 可用，因此常规增量同步只需 bot 会话。
type TelegramChannelDiffer struct {
	API       ChannelDiffAPI
	ChannelID int64

	mu      sync.Mutex
	channel *tg.Channel // bot 视角解析一次，含正确的 access_hash
}

// ChannelDifference 请求 pts 之后的频道差异。
//
// 输入：pts 本地游标；limit 单轮事件上限（普通客户端 10-100）
// 输出：差异（只有消息号）；error
// 预期：TooLong 时同样只给消息号，由上层决定整体重建
func (d *TelegramChannelDiffer) ChannelDifference(ctx context.Context, pts, limit int) (Difference, error) {
	ch, err := d.channelOf(ctx)
	if err != nil {
		return Difference{}, err
	}
	res, err := d.API.UpdatesGetChannelDifference(ctx, &tg.UpdatesGetChannelDifferenceRequest{
		Channel: &tg.InputChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash},
		Filter:  &tg.ChannelMessagesFilter{},
		Pts:     pts,
		Limit:   limit,
	})
	if err != nil {
		return Difference{}, err
	}

	switch v := res.(type) {
	case *tg.UpdatesChannelDifferenceEmpty:
		return Difference{Pts: v.Pts, Final: true}, nil
	case *tg.UpdatesChannelDifference:
		return Difference{
			Pts:     v.Pts,
			Final:   v.Final,
			Changed: idsOf(v.NewMessages, editedMessages(v.OtherUpdates)),
			Deleted: deletedIDs(v.OtherUpdates),
		}, nil
	case *tg.UpdatesChannelDifferenceTooLong:
		newPts := 0
		if dlg, ok := v.Dialog.(*tg.Dialog); ok {
			newPts = dlg.Pts
		}
		return Difference{Pts: newPts, Final: true, TooLong: true, Changed: idsOf(v.Messages, nil)}, nil
	}
	return Difference{}, fmt.Errorf(i18n.T("err.unknown_diff_type"), res)
}

// channelOf 解析并缓存 bot 视角的频道。
//
// 输入：ctx
// 输出：含 access_hash 的频道
// 预期：access_hash 与 session 绑定，换 session 后需新建 differ
func (d *TelegramChannelDiffer) channelOf(ctx context.Context) (*tg.Channel, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.channel != nil {
		return d.channel, nil
	}
	res, err := d.API.ChannelsGetChannels(ctx, []tg.InputChannelClass{
		&tg.InputChannel{ChannelID: d.ChannelID, AccessHash: 0},
	})
	if err != nil {
		return nil, err
	}
	var chats []tg.ChatClass
	switch m := res.(type) {
	case *tg.MessagesChats:
		chats = m.Chats
	case *tg.MessagesChatsSlice:
		chats = m.Chats
	}
	for _, c := range chats {
		if ch, ok := c.(*tg.Channel); ok {
			d.channel = ch
			return ch, nil
		}
	}
	return nil, fmt.Errorf(i18n.T("err.channel_unavailable"), d.ChannelID)
}

// idsOf 合并两批消息里的消息号。
//
// 输入：主消息列表、编辑事件消息列表
// 输出：去重后的消息号（保序，主列表在前）
// 预期：非普通消息（服务消息、空消息）被跳过
func idsOf(primary, edited []tg.MessageClass) []int {
	seen := map[int]bool{}
	out := make([]int, 0, len(primary)+len(edited))
	for _, list := range [][]tg.MessageClass{primary, edited} {
		for _, mc := range list {
			msg, ok := mc.(*tg.Message)
			if !ok || seen[msg.ID] {
				continue
			}
			seen[msg.ID] = true
			out = append(out, msg.ID)
		}
	}
	return out
}

// deletedIDs 收集差异里的删除事件。
//
// 输入：other_updates
// 输出：被删除的消息 ID
// 预期：兼容频道删除与普通删除两种更新
func deletedIDs(updates []tg.UpdateClass) []int {
	var ids []int
	for _, u := range updates {
		switch v := u.(type) {
		case *tg.UpdateDeleteChannelMessages:
			ids = append(ids, v.Messages...)
		case *tg.UpdateDeleteMessages:
			ids = append(ids, v.Messages...)
		}
	}
	return ids
}

// editedMessages 收集差异里的编辑事件携带的消息。
func editedMessages(updates []tg.UpdateClass) []tg.MessageClass {
	var msgs []tg.MessageClass
	for _, u := range updates {
		switch v := u.(type) {
		case *tg.UpdateEditChannelMessage:
			msgs = append(msgs, v.Message)
		case *tg.UpdateEditMessage:
			msgs = append(msgs, v.Message)
		}
	}
	return msgs
}
