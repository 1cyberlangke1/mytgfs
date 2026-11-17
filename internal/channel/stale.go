package channel

// 频道失效类错误的判定。
//
// 频道不是静态资源：可能被删、被迁移、bot 被踢出，换过 session 后
// access_hash 也会失效。这些情况返回的错误类型相同，调用方据此决定
// 「重新解析再试一次」还是「这个频道已经没了」。
//
// 判定放在 channel 包而不是 telegramstore：telegramstore 已经依赖
// channel，反向引用会成环。

import (
	"errors"

	"github.com/gotd/td/tgerr"
)

// staleHashErrors 是「access_hash 可能过期或频道已不可用」的错误集合。
var staleHashErrors = map[string]bool{
	"CHANNEL_INVALID":      true,
	"CHANNEL_PRIVATE":      true,
	"CHANNEL_INACCESSIBLE": true,
	"PEER_ID_INVALID":      true,
}

// IsStale 判断错误是否属于频道失效类。
//
// 输入：任意 error（可带包裹）
// 输出：true 表示清掉缓存的 access_hash 重新解析后可能恢复，
//
//	或频道确实已被删除
//
// 预期：只认 tgerr 的类型名；限流、权限、网络错误返回 false
func IsStale(err error) bool {
	e, ok := errors.AsType[*tgerr.Error](err)
	return ok && staleHashErrors[e.Type]
}
