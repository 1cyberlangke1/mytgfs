package trayapp

// 托盘纯逻辑的单测：不启动 systray、不连 Telegram。

import (
	"testing"
	"time"

	"mytgfs/internal/config"
	"mytgfs/internal/eventbus"
)

// TestSyncInterval 同步间隔的三档语义：负数关闭、0 用运行时默认秒数、正数原样。
func TestSyncInterval(t *testing.T) {
	cases := []struct {
		in   int
		want time.Duration
	}{
		{-1, 0},
		{-60, 0},
		{0, config.DefaultSyncSeconds * time.Second},
		{5, 5 * time.Second},
		{600, 600 * time.Second},
	}
	for _, c := range cases {
		if got := SyncInterval(c.in); got != c.want {
			t.Errorf("SyncInterval(%d) = %v，期望 %v", c.in, got, c.want)
		}
	}
}

// TestStatusNotifierNilBus 没有事件总线时返回 nil，调用方据此跳过通知。
func TestStatusNotifierNilBus(t *testing.T) {
	if fn := StatusNotifier(nil); fn != nil {
		t.Fatal("nil 总线应返回 nil")
	}
}

// TestStatusNotifierPublishesOnChange 有增删才推 status，空轮不打扰面板。
//
// 输入：订阅 status；先空轮再带变更各调一次
// 输出：只收到一次事件
func TestStatusNotifierPublishesOnChange(t *testing.T) {
	bus := eventbus.New()
	ch, cancel := bus.Subscribe("status")
	defer cancel()

	fn := StatusNotifier(bus)
	fn(nil, nil)
	if len(ch) != 0 {
		t.Fatal("空轮不应推送")
	}
	fn([]string{"/a"}, nil)
	if len(ch) != 1 {
		t.Fatalf("收到 %d 次，期望 1 次", len(ch))
	}
	fn(nil, []string{"/b"})
	if len(ch) != 2 {
		t.Fatalf("收到 %d 次，期望 2 次", len(ch))
	}
}

// TestSyncSwitchToggle 开关默认值与切换都按 atomic 语义生效。
func TestSyncSwitchToggle(t *testing.T) {
	on := NewSyncSwitch(true)
	if !on.On() {
		t.Fatal("初始应为开")
	}
	on.Set(false)
	if on.On() {
		t.Fatal("关闭后仍为开")
	}
	on.Set(true)
	if !on.On() {
		t.Fatal("重新开启失败")
	}

	off := NewSyncSwitch(false)
	if off.On() {
		t.Fatal("初始应为关")
	}
}

// TestHumanBytes 字节数按 1024 进位，整数档不带小数。
func TestHumanBytes(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1 << 20, "1.0 MB"},
		{3 << 30, "3.0 GB"},
		{1 << 40, "1.0 TB"},
	}
	for _, c := range cases {
		if got := humanBytes(c.in); got != c.want {
			t.Errorf("humanBytes(%d) = %q，期望 %q", c.in, got, c.want)
		}
	}
}

// TestStateCode 状态机器码与状态条配色解耦，未知键回落 starting。
func TestStateCode(t *testing.T) {
	cases := map[string]string{
		"tray.state.connected":    "online",
		"tray.state.connecting":   "connecting",
		"tray.state.reconnecting": "reconnecting",
		"tray.state.auth_lost":    "auth_lost",
		"tray.state.whatever":     "starting",
		"":                        "starting",
	}
	for in, want := range cases {
		if got := stateCode(in); got != want {
			t.Errorf("stateCode(%q) = %q，期望 %q", in, got, want)
		}
	}
}
