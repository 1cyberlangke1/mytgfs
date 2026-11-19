package telegramstore

// 频道失效判定单测：只有「频道没了」才允许上层摘卷。

import (
	"errors"
	"fmt"
	"testing"

	"github.com/gotd/td/tgerr"
)

// TestIsChannelGone 只有频道不可用的错误才算失效。
//
// 输入：各类 RPC 错误与包装后的同类错误
// 输出：CHANNEL_* 失效类为 true；权限类与普通网络错误为 false
// 预期：避免把可修复的权限问题误判成卷已删除
func TestIsChannelGone(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{tgerr.New(400, "CHANNEL_INVALID"), true},
		{tgerr.New(400, "CHANNEL_PRIVATE"), true},
		{tgerr.New(400, "CHANNEL_INACCESSIBLE"), true},
		{tgerr.New(400, "PEER_ID_INVALID"), true},
		{fmt.Errorf("wrap: %w", tgerr.New(400, "CHANNEL_INVALID")), true},
		{tgerr.New(403, "CHAT_WRITE_FORBIDDEN"), false},
		{tgerr.New(400, "CHANNEL_ADMIN_REQUIRED"), false},
		{tgerr.New(420, "FLOOD_WAIT_30"), false},
		{errors.New("timeout"), false},
		{nil, false},
	}
	for _, c := range cases {
		if got := IsChannelGone(c.err); got != c.want {
			t.Errorf("IsChannelGone(%v) = %v, want %v", c.err, got, c.want)
		}
	}
}
