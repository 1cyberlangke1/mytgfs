package trayapp

// 托盘纯逻辑的单测：不启动 systray、不连 Telegram。

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"mytgfs/internal/config"
	"mytgfs/internal/push"
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

// TestStatusNotifierNilEvents 没有推送通道时返回 nil，调用方据此跳过通知。
func TestStatusNotifierNilEvents(t *testing.T) {
	if fn := StatusNotifier(nil); fn != nil {
		t.Fatal("nil 通道应返回 nil")
	}
}

// TestStatusNotifierPublishesOnChange 每个回调都推 status，含只有封面变化的空列表。
//
// 输入：记录器；空列表与带变更依次调用
// 输出：三次都推送（调用方已经过滤了真正无变化的轮次）
func TestStatusNotifierPublishesOnChange(t *testing.T) {
	rec := push.NewRecorder()
	fn := StatusNotifier(rec)

	fn(nil, nil)
	if got := rec.Count(push.TopicStatus); got != 1 {
		t.Fatalf("封面变化用空列表表达，应推送，实际 %d", got)
	}
	fn([]string{"/a"}, nil)
	fn(nil, []string{"/b"})
	if got := rec.Count(push.TopicStatus); got != 3 {
		t.Fatalf("推送 %d 次，期望 3 次", got)
	}
}

// TestSyncEnabledReadsConfig 自动同步开关每轮重新读配置，读失败回退启动值。
//
// 输入：临时配置里 auto_sync 的 true/false 与一份损坏文件
// 输出：SyncEnabled 返回的判断结果
// 预期：面板改设置后无需重启即可生效；配置坏掉时保持启动时的行为
func TestSyncEnabledReadsConfig(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte("[mount]\nauto_sync = false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	on := SyncEnabled(p, true)
	if on() {
		t.Fatal("auto_sync=false 仍返回开启")
	}
	if err := os.WriteFile(p, []byte("[mount]\nauto_sync = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !on() {
		t.Fatal("auto_sync=true 未生效")
	}
	if err := os.WriteFile(p, []byte("not = [toml"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !SyncEnabled(p, true)() {
		t.Fatal("损坏配置未回退到 fallback=true")
	}
	if SyncEnabled(p, false)() {
		t.Fatal("损坏配置未回退到 fallback=false")
	}
}

// TestVolumeMenuLabel 托盘卷入口显示「卷名 · 已用容量」。
func TestVolumeMenuLabel(t *testing.T) {
	cases := []struct {
		name string
		used int64
		want string
	}{
		{"demo", 166787473, "demo · 159.1 MB"},
		{"demo2", 8192, "demo2 · 8.0 KB"},
	}
	for _, c := range cases {
		if got := volumeMenuLabel(c.name, c.used); got != c.want {
			t.Errorf("volumeMenuLabel(%q, %d) = %q，期望 %q", c.name, c.used, got, c.want)
		}
	}
}

// TestVolumeURL 快速打开地址带 tab/vol 且卷名经过转义。
func TestVolumeURL(t *testing.T) {
	if got := volumeURL("demo2"); got != "/?tab=files&vol=demo2" {
		t.Errorf("volumeURL = %q", got)
	}
	if got := volumeURL("a b&c"); got != "/?tab=files&vol=a+b%26c" {
		t.Errorf("volumeURL 未转义: %q", got)
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
