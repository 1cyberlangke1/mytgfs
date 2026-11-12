//go:build !linux && !windows && !darwin

package tray

// 其它平台没有 systray 支持，一律按无图形环境处理。

// Supported 报告当前环境能不能起托盘。
//
// 输入：无
// 输出：false
// 预期：调用方应退回无托盘模式
func Supported() bool { return false }
