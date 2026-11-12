//go:build !windows

package vfs

// installConsoleCloseHandler 在非 Windows 平台上是空实现。
//
// 输入：unmount 卸载函数（忽略）
// 输出：无
// 预期：Linux/macOS 由 ctx 取消或信号触发卸载，不需要控制台事件
func installConsoleCloseHandler(unmount func()) {}
