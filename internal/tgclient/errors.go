package tgclient

// 授权失效判定：把 gotd 的多处错误形态收敛成一个判断。

import (
	"errors"

	"github.com/gotd/td/telegram"
)

// IsAuthLost 判断错误是否表示授权已失效。
//
// 输入：任意 error（可为 nil）
// 输出：true 表示会话被吊销/退出登录/账号停用/授权 key 被复制等，只能重新登录
// 预期：同时识别 gotd 的 telegram.IsAuthLost 与 *telegram.AuthLostError，
//
//	这样业务层不用关心错误是 RPC 直接返回还是 Run 退出时包出来的
func IsAuthLost(err error) bool {
	if err == nil {
		return false
	}
	if telegram.IsAuthLost(err) {
		return true
	}
	var lost *telegram.AuthLostError
	return errors.As(err, &lost)
}
