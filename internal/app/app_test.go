package app

import (
	"testing"

	"mytgfs/internal/config"
)

// TestVolumeResolution 覆盖卷名解析：空名取首个、无 [[volumes]] 段退化为 mytgfs。
//
// 输入：构造好的 config.Config 与命令行给的卷名
// 输出：解析出的卷名、卷定义、是否存在
// 预期：无卷段时只有 mytgfs 可解析；有卷段时空名取第一个，未知名报不存在
func TestVolumeResolution(t *testing.T) {
	multi := &App{cfg: config.Config{
		Telegram: config.Telegram{ChannelID: 111},
		Volumes: []config.Volume{
			{Name: "demo", ChatID: 222},
			{Name: "demo2", ChatID: 333},
		},
	}}
	single := &App{cfg: config.Config{Telegram: config.Telegram{ChannelID: 111}}}

	cases := []struct {
		name     string
		app      *App
		input    string
		wantName string
		wantChat int64
		wantOK   bool
	}{
		{"空名取第一个卷", multi, "", "demo", 222, true},
		{"按名取卷", multi, "demo2", "demo2", 333, true},
		{"未知名", multi, "nope", "nope", 0, false},
		{"无卷段退化默认名", single, "", "mytgfs", 111, true},
		{"无卷段显式默认名", single, "mytgfs", "mytgfs", 111, true},
		{"无卷段其他名", single, "demo", "demo", 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			vol, gotName, ok := c.app.Volume(c.input)
			if gotName != c.wantName {
				t.Fatalf("卷名 = %q，期望 %q", gotName, c.wantName)
			}
			if ok != c.wantOK {
				t.Fatalf("ok = %v，期望 %v", ok, c.wantOK)
			}
			if ok && (vol.ChatID != c.wantChat || vol.Name != c.wantName) {
				t.Fatalf("卷 = %+v，期望 name=%q chat=%d", vol, c.wantName, c.wantChat)
			}
		})
	}
}
