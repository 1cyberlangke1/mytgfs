//go:build windows

package netmap

// net.exe 是控制台程序。托盘与面板跑在 GUI 子系统进程里，没有可继承的控制台，
// 直接起子进程时 Windows 会为它新开一个控制台窗口，用户会看到黑框闪一下。
// 这里统一带 CREATE_NO_WINDOW（HideWindow），让映射清理对用户不可见。

import (
	"os/exec"
	"syscall"
)

// command 构造隐藏窗口的 net 命令。
//
// 输入：net 的子命令与参数
// 输出：配置了 HideWindow 的 *exec.Cmd
// 预期：子进程不分配控制台窗口
func command(args ...string) *exec.Cmd {
	cmd := exec.Command("net", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd
}
