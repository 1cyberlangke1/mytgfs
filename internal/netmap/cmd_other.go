//go:build !windows

package netmap

// 非 Windows 平台没有盘符映射，Cleanup 会直接返回；
// 这个实现只是为了让包在两个平台上都能编译。

import "os/exec"

// command 构造 net 命令（非 Windows 路径不会真正执行）。
func command(args ...string) *exec.Cmd {
	return exec.Command("net", args...)
}
