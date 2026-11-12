package daemon

// 进程分离的跨平台封装。

import (
	"os/exec"
)

// newRunCommand 构造 `mytgfs daemon run` 命令。
func newRunCommand(exe string) *exec.Cmd {
	return exec.Command(exe, "daemon", "run")
}
