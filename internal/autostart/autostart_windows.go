//go:build windows

package autostart

// Windows 开机自启：写当前用户的 Run 键，不需要管理员权限。

import (
	"errors"
	"strings"

	"golang.org/x/sys/windows/registry"
)

const (
	runKey    = `Software\Microsoft\Windows\CurrentVersion\Run`
	valueName = "mytgfs"
)

// Enable 在 HKCU Run 键写入启动命令。
//
// 输入：exe 可执行文件绝对路径
// 输出：error
// 预期：只影响当前用户；重复启用会覆盖旧命令；同目录有 mytgfs-tray.exe 时\n//       优先注册 GUI 版，登录时不弹控制台
func Enable(exe string) error {
	exe = PreferTray(exe)
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	return k.SetStringValue(valueName, commandLine(exe))
}

// Disable 删除 Run 键里的自启项。
//
// 输入：无
// 输出：error
// 预期：本来就不存在时视为成功
func Disable() error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if err := k.DeleteValue(valueName); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	return nil
}

// Enabled 读取 Run 键判断是否已开启。
//
// 输入：无
// 输出：是否已注册；读取失败返回 error
// 预期：值存在且非空即视为开启
func Enabled() (bool, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer k.Close()
	v, _, err := k.GetStringValue(valueName)
	if errors.Is(err, registry.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(v) != "", nil
}

// commandLine 拼接 Windows 启动命令行。
//
// 输入：exe 绝对路径
// 输出：命令行字符串
// 预期：含空格或制表符的参数用双引号包住（Program Files 场景）
func commandLine(exe string) string {
	parts := Command(exe)
	for i, p := range parts {
		if strings.ContainsAny(p, " \t") {
			parts[i] = `"` + p + `"`
		}
	}
	return strings.Join(parts, " ")
}
