package channel

// 频道失效类错误判定的单测。

import (
	"errors"
	"fmt"
	"testing"

	"github.com/gotd/td/tgerr"
)

// TestIsStale 验证只有频道失效类错误被认出来。
//
// 输入：构造的 *tgerr.Error、包裹错误、普通错误
// 输出：无；断言失败时 t.Error
// 预期：四个 stale 类型为 true；限流、权限、普通错误与 nil 为 false
func TestIsStale(t *testing.T) {
	for _, typ := range []string{"CHANNEL_INVALID", "CHANNEL_PRIVATE", "CHANNEL_INACCESSIBLE", "PEER_ID_INVALID"} {
		if !IsStale(&tgerr.Error{Code: 400, Message: typ, Type: typ}) {
			t.Errorf("%s 应判定为失效", typ)
		}
	}
	for _, typ := range []string{"FLOOD_WAIT", "CHAT_WRITE_FORBIDDEN", "CHANNEL_ADMIN_REQUIRED", "MESSAGE_ID_INVALID"} {
		if IsStale(&tgerr.Error{Code: 400, Message: typ, Type: typ}) {
			t.Errorf("%s 不该判定为失效", typ)
		}
	}
	if IsStale(nil) {
		t.Error("nil 不该判定为失效")
	}
	if IsStale(errors.New("boom")) {
		t.Error("普通错误不该判定为失效")
	}
	// 包裹后的错误也要能认出来：调用方经常 fmt.Errorf("%w") 再返回
	wrapped := fmt.Errorf("delete channel: %w", &tgerr.Error{Code: 400, Message: "CHANNEL_INVALID", Type: "CHANNEL_INVALID"})
	if !IsStale(wrapped) {
		t.Error("被包裹的失效错误应判定为失效")
	}
}
