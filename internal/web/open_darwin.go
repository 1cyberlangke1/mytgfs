//go:build darwin

package web

// macOS：用 Finder 打开目录。

import "os/exec"

func openDir(dir string) error {
	return exec.Command("open", dir).Start()
}
