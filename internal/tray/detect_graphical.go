//go:build windows || darwin

package tray

// Windows 与 macOS 始终有图形会话（服务/无头场景不在支持范围内）。

// Supported 报告当前环境能不能起托盘。
//
// 输入：无
// 输出：true
// 预期：调用方正常起 systray
func Supported() bool { return true }
