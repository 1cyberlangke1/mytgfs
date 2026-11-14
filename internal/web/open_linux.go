//go:build linux

package web

// Linux：交给桌面环境的默认文件管理器。

import "os/exec"

func openDir(dir string) error {
	return exec.Command("xdg-open", dir).Start()
}
