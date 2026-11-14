//go:build darwin

package web

// macOS：用 Finder 打开目录，或交给默认程序打开文件。

import "os/exec"

func openPath(dir string) error {
	return exec.Command("open", dir).Start()
}
