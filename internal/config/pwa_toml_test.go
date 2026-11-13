package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestPWATomlRoundTrip 确认 [pwa] 段能从真实 TOML 读进来。
//
// *bool 用来区分「没写」与「显式 false」：toml 库必须能解析 false。
func TestPWATomlRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	content := "[pwa]\nenabled = false\nname = \"My Drive\"\noffline_max_mb = 64\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PWA.On() {
		t.Fatal("enabled=false was ignored")
	}
	if cfg.PWA.AppName() != "My Drive" {
		t.Fatalf("AppName=%q", cfg.PWA.AppName())
	}
	if cfg.PWA.OfflineLimitMB() != 64 {
		t.Fatalf("OfflineLimitMB=%d", cfg.PWA.OfflineLimitMB())
	}
}

// TestPWATomlAbsentUsesDefaults 没有 [pwa] 段时全部走好用默认。
func TestPWATomlAbsentUsesDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("[mount]\npoint = \"Y:\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.PWA.On() {
		t.Fatal("default should be enabled")
	}
	if cfg.PWA.AppName() != DefaultPWAName {
		t.Fatalf("default name=%q", cfg.PWA.AppName())
	}
	if cfg.PWA.OfflineLimitMB() != DefaultPWAOfflineMB {
		t.Fatalf("default limit=%d", cfg.PWA.OfflineLimitMB())
	}
}
