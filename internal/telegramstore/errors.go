package telegramstore

// 错误定义与外部状态变化的判定。
//
// 频道不是静态资源：可能被删、被迁移、bot 被踢出、被降权、消息被手动删除、
// 本机 session 换过导致 access_hash 过期。这里统一识别这些错误，
// 一是给出能照着做的提示，二是把缓存的频道信息丢掉让下一次调用重新解析。
// 文案全部走 i18n，语言包缺失时回落到 key，不会因为翻译缺失而崩。

import (
	"errors"
	"fmt"

	"github.com/gotd/td/tgerr"

	"mytgfs/internal/i18n"
)

var (
	// ErrTooLarge 表示文件超过单条 Telegram 消息上限。
	ErrTooLarge = errors.New(i18n.T("err.too_large"))
	// ErrNoDocument 表示 Updates 里没有 document。
	ErrNoDocument = errors.New(i18n.T("err.no_document"))
	// ErrNoMessage 表示 Updates 里没有消息 ID。
	ErrNoMessage = errors.New(i18n.T("err.no_message"))
	// ErrBadRange 表示读取区间越界。
	ErrBadRange = errors.New(i18n.T("err.bad_range"))
)

// channelErrors 把「频道这一侧不可用」的 RPC 错误映射到 i18n key。
//
// 这些错误重试同一个请求不会好转，必须让用户去 Telegram 侧修复。
var channelErrors = map[string]string{
	"CHANNEL_INVALID":          "err.channel_invalid",
	"CHANNEL_PRIVATE":          "err.channel_private",
	"CHANNEL_INACCESSIBLE":     "err.channel_inaccessible",
	"CHAT_WRITE_FORBIDDEN":     "err.chat_write_forbidden",
	"CHANNEL_ADMIN_REQUIRED":   "err.channel_admin_required",
	"USER_BANNED_IN_CHANNEL":   "err.user_banned_in_channel",
	"CHAT_FORWARDS_RESTRICTED": "err.chat_forwards_restricted",
	"PEER_ID_INVALID":          "err.peer_id_invalid",
	"MESSAGE_DELETE_FORBIDDEN": "err.message_delete_forbidden",
}

// messageGoneErrors 是「消息已经不在了」的错误集合。
var messageGoneErrors = map[string]bool{
	"MESSAGE_ID_INVALID":         true,
	"MSG_ID_INVALID":             true,
	"CHANNEL_MESSAGE_ID_INVALID": true,
}

// IsChannelUnavailable 判断错误是否表示频道侧不可用。
//
// 输入：任意 error
// 输出：true 表示需要用户去 Telegram 侧处理（重建频道、加回 bot、恢复权限）
// 预期：只认 RPC 错误类型，不匹配错误文本
func IsChannelUnavailable(err error) bool {
	e, ok := errors.AsType[*tgerr.Error](err)
	if !ok {
		return false
	}
	_, ok = channelErrors[e.Type]
	return ok
}

// IsMessageGone 判断错误是否表示消息已被删除。
//
// 输入：任意 error
// 输出：true 表示文件消息在 Telegram 侧已不存在
// 预期：调用方应把索引里的这条记录删掉，而不是反复重试
func IsMessageGone(err error) bool {
	e, ok := errors.AsType[*tgerr.Error](err)
	if !ok {
		return false
	}
	return messageGoneErrors[e.Type]
}

// DescribeError 给不可恢复的错误加上可操作提示。
//
// 输入：任意 error
// 输出：原错误；频道不可用时包一层本地化说明
// 预期：其它错误原样返回，不改变调用方的判断
func DescribeError(err error) error {
	if err == nil {
		return nil
	}
	if e, ok := errors.AsType[*tgerr.Error](err); ok {
		if key, ok := channelErrors[e.Type]; ok {
			return fmt.Errorf("%s: %w", i18n.T("err.channel_hint", i18n.T(key), e.Type), err)
		}
	}
	return err
}

// noteErr 在频道不可用时丢弃缓存的频道信息。
//
// 输入：RPC 返回的 error
// 输出：同一个 error（原样返回，方便链式使用）
// 预期：下一次调用 resolve 会重新解析 access_hash，换 session 或权限变化后自动恢复
func (s *Store) noteErr(err error) error {
	if err != nil && IsChannelUnavailable(err) {
		s.dropChannel()
	}
	return err
}

// dropChannel 清空缓存的频道信息。
func (s *Store) dropChannel() {
	s.mu.Lock()
	s.channel = nil
	s.mu.Unlock()
}
