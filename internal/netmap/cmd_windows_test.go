//go:build windows

package netmap

// Windows 专属：确认清理映射时不会给用户弹出控制台窗口。

import "testing"

// TestCommandHidesWindow net 子进程必须带 HideWindow。
//
// 输入：无
// 输出：无
// 预期：SysProcAttr 非空且 HideWindow=true；
// 否则 GUI 托盘进程调用 net use 时会闪一个黑框。
func TestCommandHidesWindow(t *testing.T) {
	cmd := command("use")
	if cmd.SysProcAttr == nil {
		t.Fatal("SysProcAttr 为空，子进程会新开控制台窗口")
	}
	if !cmd.SysProcAttr.HideWindow {
		t.Error("HideWindow 未开启")
	}
	if len(cmd.Args) == 0 || cmd.Args[0] != "net" {
		t.Errorf("命令 = %v", cmd.Args)
	}
}
