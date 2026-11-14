//go:build windows

package web

// Windows：用资源管理器打开目录。
//
// 输入：dir 绝对路径
// 输出：error；进程启动失败时返回
// 预期：不打开控制台窗口；explorer 会自动把已存在的窗口切到前台

import (
	"os/exec"
	"syscall"
)

func openDir(dir string) error {
	cmd := exec.Command("explorer.exe", dir)
	// GUI 子系统进程起控制台子进程会闪黑框，这里显式隐藏
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd.Start()
}
