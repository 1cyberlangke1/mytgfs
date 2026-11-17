package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/gotd/td/tgerr"

	"mytgfs/internal/i18n"
)

// TestCheckFailure 验证「频道失效」类错误被换成可读说明，其它错误原样返回。
//
// 输入：构造的 *tgerr.Error 与普通 error
// 输出：无；断言失败时 t.Error
// 预期：CHANNEL_INVALID 走 err.channel_not_visible 且带频道号；其它错误保持同一实例
func TestCheckFailure(t *testing.T) {
	gone := &tgerr.Error{Code: 400, Message: "CHANNEL_INVALID", Type: "CHANNEL_INVALID"}
	got := checkFailure(4356528285, gone)
	if strings.Contains(got.Error(), "CHANNEL_INVALID") {
		t.Fatalf("原始 RPC 文本不该透出: %v", got)
	}
	if !strings.Contains(got.Error(), "4356528285") {
		t.Fatalf("缺少频道号: %v", got)
	}
	if want := i18n.T("err.channel_unusable", 4356528285); got.Error() != want {
		t.Fatalf("got %q want %q", got.Error(), want)
	}

	plain := errors.New("boom")
	if got := checkFailure(1, plain); got != plain {
		t.Fatalf("非频道失效错误应原样返回, got %v", got)
	}

	flood := &tgerr.Error{Code: 420, Message: "FLOOD_WAIT_5", Type: "FLOOD_WAIT", Argument: 5}
	if got := checkFailure(1, flood); got != flood {
		t.Fatalf("限流错误不该被改写, got %v", got)
	}
}
