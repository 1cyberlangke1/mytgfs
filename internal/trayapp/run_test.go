package trayapp

// 启动前置校验：配置或凭据缺失时直接返回错误，不进入 GUI 初始化。

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRunRejectsMissingConfig 配置读不到时返回错误，不弹窗口。
func TestRunRejectsMissingConfig(t *testing.T) {
	p := filepath.Join(t.TempDir(), "missing.toml")
	if err := Run(Options{ConfigPath: p}); err == nil {
		t.Fatal("缺失配置应返回错误")
	}
}

// TestRunRejectsMissingAPIHash 有配置但没 api_hash 时返回错误，不进入连接流程。
func TestRunRejectsMissingAPIHash(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte("[telegram]\napi_id = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Run(Options{ConfigPath: p}); err == nil {
		t.Fatal("缺失 api_hash 应返回错误")
	}
}
