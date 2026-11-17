//go:build e2e

package main

// 安卓 e2e 的配置引导：设备上没有预先放好的 config.toml，
// 这里写一份只有假 api_id / api_hash 的最小配置，让壳走到正常启动路径。

import (
	"os"
	"path/filepath"
	"runtime"

	"mytgfs/internal/config"
)

// prepareE2EConfig 在安卓 e2e 包里补一份最小配置。
//
// 输入：无（读当前工作目录，安卓已在 prepareSandbox 之后切到应用私有目录）
// 输出：error；非安卓或已有配置文件时返回 nil
// 预期：只写 api_id/api_hash，不连真实账号；面板由 e2eandroid 标签挂假数据层
func prepareE2EConfig() error {
	if runtime.GOOS != "android" {
		return nil
	}
	if _, err := os.Stat(config.DefaultConfigPath); err == nil {
		return nil
	}
	if err := os.MkdirAll("secrets", 0o755); err != nil {
		return err
	}
	hash := filepath.Join("secrets", "api_hash")
	if err := os.WriteFile(hash, []byte("0123456789abcdef0123456789abcdef\n"), 0o600); err != nil {
		return err
	}
	cfg := "[telegram]\n" +
		"api_id = 1\n" +
		"api_hash_file = \"" + hash + "\"\n" +
		"bot_token_file = \"\"\n\n" +
		"[mount]\n" +
		"sync_seconds = 15\n"
	return os.WriteFile(config.DefaultConfigPath, []byte(cfg), 0o600)
}
