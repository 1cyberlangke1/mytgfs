package tgclient

// tgclient 纯函数单测：代理解析、连接状态映射、授权失效判定。
//
// 不建立任何真实网络连接：resolverFor 只构造拨号器，New 只组装 client。

import (
	"errors"
	"testing"

	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
)

// TestResolverForEmpty 空地址不启用代理，返回 nil 解析器。
func TestResolverForEmpty(t *testing.T) {
	r, err := resolverFor("")
	if err != nil {
		t.Fatalf("resolverFor(\"\"): %v", err)
	}
	if r != nil {
		t.Fatalf("空地址解析器 = %v，期望 nil", r)
	}
}

// TestResolverForValid 合法 host:port 构造出非 nil 的 SOCKS5 解析器。
func TestResolverForValid(t *testing.T) {
	r, err := resolverFor("127.0.0.1:7890")
	if err != nil {
		t.Fatalf("resolverFor: %v", err)
	}
	if r == nil {
		t.Fatal("合法地址解析器为 nil")
	}
}

// TestMapConnState nil 回调映射为 nil；三种状态各归其位。
func TestMapConnState(t *testing.T) {
	if got := mapConnState(nil); got != nil {
		t.Fatal("nil 回调应映射为 nil")
	}

	var got []ConnState
	fn := mapConnState(func(s ConnState) { got = append(got, s) })
	fn(telegram.ConnectionStateConnecting)
	fn(telegram.ConnectionStateReady)
	fn(telegram.ConnectionStateDisconnected)
	want := []ConnState{ConnConnecting, ConnReady, ConnDisconnected}
	if len(got) != len(want) {
		t.Fatalf("映射结果 = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("第 %d 个状态 = %v，期望 %v", i, got[i], want[i])
		}
	}
}

// TestIsAuthLost nil 与普通错误为 false，授权失效错误为 true。
func TestIsAuthLost(t *testing.T) {
	if IsAuthLost(nil) {
		t.Error("nil 被判为授权失效")
	}
	if IsAuthLost(errors.New("boom")) {
		t.Error("普通错误被判为授权失效")
	}
	lost := &telegram.AuthLostError{Err: errors.New("AUTH_KEY_UNREGISTERED")}
	if !IsAuthLost(lost) {
		t.Error("AuthLostError 未被识别")
	}
}

// TestSetDefaultProxy 进程默认代理解析进 New 的构造流程，且可清空。
func TestSetDefaultProxy(t *testing.T) {
	SetDefaultProxy("127.0.0.1:7890")
	t.Cleanup(func() { SetDefaultProxy("") })

	c := New(1, "hash", &session.StorageMemory{}, Options{})
	if c == nil || c.Client == nil || c.waiter == nil {
		t.Fatalf("New 返回不完整: %+v", c)
	}
	// 清空默认代理后再构造一次，确认不会因残留状态失败
	SetDefaultProxy("")
	if c2 := New(1, "hash", &session.StorageMemory{}, Options{}); c2 == nil {
		t.Fatal("清空代理后 New 返回 nil")
	}
}
