package channel

// messages.getChats 的结果有两种形态（普通与 slice）。
// 调用方只关心聊天列表，这里统一取出，避免每个调用点各写一遍类型分支。

import "github.com/gotd/td/tg"

// ChatsOf 取出 messages.getChats 结果里的聊天列表。
//
// 输入：res getChats 的返回值（MessagesChats / MessagesChatsSlice）
// 输出：聊天列表；未识别的类型返回 nil
// 预期：只做类型兼容，不改变顺序，也不过滤内容
func ChatsOf(res tg.MessagesChatsClass) []tg.ChatClass {
	switch m := res.(type) {
	case *tg.MessagesChats:
		return m.Chats
	case *tg.MessagesChatsSlice:
		return m.Chats
	}
	return nil
}
