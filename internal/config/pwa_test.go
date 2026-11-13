package config

import "testing"

// PWA 默认与夹取规则：未配置即启用，显式 false 才关闭。

func TestPWAOn(t *testing.T) {
	off := false
	on := true
	cases := []struct {
		name string
		p    PWA
		want bool
	}{
		{"zero value defaults on", PWA{}, true},
		{"explicit off", PWA{Enabled: &off}, false},
		{"explicit on", PWA{Enabled: &on}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.p.On(); got != c.want {
				t.Fatalf("On()=%v want %v", got, c.want)
			}
		})
	}
}

func TestPWAAppName(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", DefaultPWAName},
		{"   ", DefaultPWAName},
		{"  My Drive  ", "My Drive"},
	}
	for _, c := range cases {
		if got := (PWA{Name: c.in}).AppName(); got != c.want {
			t.Fatalf("AppName(%q)=%q want %q", c.in, got, c.want)
		}
	}
}

func TestPWAOfflineLimit(t *testing.T) {
	cases := []struct {
		name   string
		mb     int
		wantMB int
		wantB  int64
	}{
		{"zero uses default", 0, DefaultPWAOfflineMB, int64(DefaultPWAOfflineMB) << 20},
		{"negative means unlimited", -1, 0, 0},
		{"explicit value", 64, 64, 64 << 20},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := PWA{OfflineMaxMB: c.mb}
			if got := p.OfflineLimitMB(); got != c.wantMB {
				t.Fatalf("OfflineLimitMB()=%d want %d", got, c.wantMB)
			}
			if got := p.OfflineLimitBytes(); got != c.wantB {
				t.Fatalf("OfflineLimitBytes()=%d want %d", got, c.wantB)
			}
		})
	}
}
