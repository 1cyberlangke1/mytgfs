//go:build windows

package web

// Windows：用资源管理器打开目录，或交给默认程序打开文件。
//
// 输入：path 绝对路径
// 输出：error；进程启动失败时返回
// 预期：不设置 HideWindow —— explorer 是 GUI 进程，设置后连新建的文件夹窗口
// 都会被一起隐藏（实测 9 次点击留下 9 个不可见窗口），表现为「点了没反应」。

import "os/exec"

func openPath(dir string) error {
	cmd := exec.Command("explorer.exe", dir)
	return cmd.Start()
}
