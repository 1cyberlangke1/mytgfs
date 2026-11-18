//go:build !linux

package web

// 非 Linux 平台没有 /proc/self/fd，宿主给的路径就是普通文件路径。

import "os"

// openHostFile 直接打开宿主给的路径。
//
// 输入：p 绝对路径
// 输出：*os.File；错误时返回系统错误
// 预期：Windows/macOS 的 adopt 只接受真实路径，不涉及 fd 重开
func openHostFile(p string) (*os.File, error) {
	return os.Open(p)
}
