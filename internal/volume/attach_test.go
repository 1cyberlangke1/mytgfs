package volume

// 挂载已有频道时 chat_id 归一化的单元测试。

import "testing"

// TestNormalizeChatID Bot API 形式与裸 id 都要还原成频道 id。
//
// 输入：-100... / 正数 / 非法值
// 输出：频道 id
// 预期：-100 前缀按 -1000000000000 - channelID 反算；0 与负数（非 -100）返回 0
func TestNormalizeChatID(t *testing.T) {
	cases := []struct{ in, want int64 }{
		{-1003536007396, 3536007396},
		{3536007396, 3536007396},
		{0, 0},
		{-123, 0},
	}
	for _, c := range cases {
		if got := NormalizeChatID(c.in); got != c.want {
			t.Errorf("NormalizeChatID(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

// TestAttachRejectsEmptyName 空卷名直接拒绝，不发 RPC。
func TestAttachRejectsEmptyName(t *testing.T) {
	if _, err := Attach(t.Context(), nil, AttachRequest{Name: "  ", ChatID: -1001}); err == nil {
		t.Fatal("空卷名应报错")
	}
}

// TestAttachRejectsBadChatID 非法 chat_id 直接拒绝。
func TestAttachRejectsBadChatID(t *testing.T) {
	if _, err := Attach(t.Context(), nil, AttachRequest{Name: "x", ChatID: 0}); err == nil {
		t.Fatal("非法 chat_id 应报错")
	}
}
