//go:build !windows

package daemon

// Unix 进程分离。

import (
	"os/exec"
	"syscall"
)

// detached 让子进程成为新会话首领。
func detached(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
