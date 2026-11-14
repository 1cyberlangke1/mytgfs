package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDownloadsTomlRoundTrip 确认 [downloads] dir 能从真实 TOML 读进来。
//
// 输入：写有 [downloads] dir = "D:/dl" 的配置文件
// 输出：cfg.Downloads.Dir 等于该值
// 预期：段落缺失时为零值，由调用方回落到 data/downloads
func TestDownloadsTomlRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("[downloads]\ndir = \"D:/dl\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Downloads.Dir != "D:/dl" {
		t.Fatalf("downloads.dir=%q", cfg.Downloads.Dir)
	}
}

// TestDownloadsTomlAbsent 没有 [downloads] 段时为零值。
//
// 输入：只有 [mount] 的配置文件
// 输出：cfg.Downloads.Dir == ""
// 预期：由组装层决定默认目录，配置层不猜绝对路径
func TestDownloadsTomlAbsent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("[mount]\nsync_seconds = 15\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Downloads.Dir != "" {
		t.Fatalf("downloads.dir=%q", cfg.Downloads.Dir)
	}
}
