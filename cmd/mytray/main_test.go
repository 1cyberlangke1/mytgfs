package main

// 桌面入口的启动前置：工作目录不切换，且托盘不允许在缺凭证时弹设置页。

import "testing"

// TestPrepareSandboxDesktopNoop 桌面启动不做沙盒切换。
func TestPrepareSandboxDesktopNoop(t *testing.T) {
	if err := prepareSandbox(); err != nil {
		t.Fatalf("桌面 prepareSandbox 应为 nil，实际 %v", err)
	}
}

// TestSetupAllowedDesktopFalse 托盘入口缺凭证时应报错，而不是弹设置页。
func TestSetupAllowedDesktopFalse(t *testing.T) {
	if setupAllowed {
		t.Fatal("桌面托盘的 setupAllowed 必须为 false")
	}
}
