//go:build linux

package web

// 安卓/Linux 上 /proc/self/fd/N 只能 stat，不能再 open：
// 系统会拦下对这条符号链接的 open()，所以 SAF 的 fd 必须 dup 后用 os.NewFile 包。

import (
	"os"
	"strconv"
	"strings"
	"syscall"
)

// probeFdPrefix 是宿主传进来的 fd 路径前缀。
const probeFdPrefix = "/proc/self/fd/"

// openHostFile 打开宿主给的路径。
//
// 输入：p 绝对路径，可能是 /proc/self/fd/N
// 输出：*os.File；错误时返回系统错误
// 预期：/proc/self/fd/N 走 dup，避免 Android 拒绝 open；dup 出来的句柄
//
//	归本函数所有，关闭它不会影响宿主手里的原 fd
func openHostFile(p string) (*os.File, error) {
	n, err := strconv.Atoi(strings.TrimPrefix(p, probeFdPrefix))
	if !strings.HasPrefix(p, probeFdPrefix) || err != nil {
		return os.Open(p)
	}
	dup, err := syscall.Dup(n)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(dup), p), nil
}
