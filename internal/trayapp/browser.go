package trayapp

// 用系统默认浏览器打开 URL；三平台各用各自的方式。

import (
	"os/exec"
	"runtime"
)

// OpenBrowser 尝试用系统默认浏览器打开 URL。
//
// 输入：url 完整地址
// 输出：无
// 预期：启动失败时静默忽略，不影响托盘与挂载
func OpenBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}
