//go:build linux

package autostart

// Linux 开机自启：XDG Autostart（~/.config/autostart/*.desktop）。

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"mytgfs/internal/atomicfile"
)

// desktopPath 返回当前用户的 autostart 文件路径。
func desktopPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "autostart", "mytgfs.desktop"), nil
}

// Enable 写入 autostart 的 .desktop 文件。
//
// 输入：exe 可执行文件绝对路径
// 输出：error
// 预期：文件已存在时覆盖；桌面环境登录后自动拉托盘；同目录有 mytgfs-tray\n//       时优先注册它
func Enable(exe string) error {
	exe = PreferTray(exe)
	p, err := desktopPath()
	if err != nil {
		return err
	}
	return atomicfile.WriteFile(p, []byte(desktopEntry(exe)), 0o644)
}

// Disable 删除 autostart 文件。
//
// 输入：无
// 输出：error
// 预期：文件本来不存在时视为成功
func Disable() error {
	p, err := desktopPath()
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Enabled 判断 autostart 文件是否存在。
func Enabled() (bool, error) {
	p, err := desktopPath()
	if err != nil {
		return false, err
	}
	if _, err := os.Stat(p); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// desktopEntry 生成 Desktop Entry 内容。
//
// 输入：exe 可执行文件路径
// 输出：.desktop 文本
// 预期：Exec 使用绝对路径，Terminal=false，GNOME 下显式开启
func desktopEntry(exe string) string {
	var b strings.Builder
	b.WriteString("[Desktop Entry]\n")
	b.WriteString("Type=Application\n")
	b.WriteString("Name=mytgfs\n")
	b.WriteString("Comment=Telegram as a drive\n")
	fmt.Fprintf(&b, "Exec=%s\n", desktopExec(Command(exe)))
	b.WriteString("Terminal=false\n")
	b.WriteString("X-GNOME-Autostart-enabled=true\n")
	return b.String()
}

// desktopExec 按 Desktop Entry 规范转义命令行参数。
//
// 输入：args 命令与参数
// 输出：空格分隔的带引号命令
// 预期：反斜杠、双引号、反引号、美元符号都转义，路径含空格也安全
func desktopExec(args []string) string {
	esc := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "`", "\\`", `$`, `\$`)
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = `"` + esc.Replace(a) + `"`
	}
	return strings.Join(out, " ")
}
