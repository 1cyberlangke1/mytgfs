//go:build linux

package web

// Linux：交给桌面环境的默认文件管理器或默认程序。

import "os/exec"

func openPath(dir string) error {
	return exec.Command("xdg-open", dir).Start()
}
