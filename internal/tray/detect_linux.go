//go:build linux

package tray

// Linux 上用 DISPLAY / WAYLAND_DISPLAY 判断有没有图形会话。

import "os"

// Supported 报告当前环境能不能起托盘。
//
// 输入：无（读环境变量 DISPLAY / WAYLAND_DISPLAY）
// 输出：有图形会话返回 true
// 预期：纯 SSH / systemd 服务等无图形环境返回 false，调用方退回无托盘模式
func Supported() bool {
	return os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != ""
}
