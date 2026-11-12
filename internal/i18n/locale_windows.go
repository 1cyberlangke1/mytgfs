//go:build windows

package i18n

// Windows 平台：用 kernel32.GetUserDefaultLocaleName 读系统语言。

import (
	"syscall"
	"unsafe"
)

var (
	kernel32                     = syscall.NewLazyDLL("kernel32.dll")
	procGetUserDefaultLocaleName = kernel32.NewProc("GetUserDefaultLocaleName")
)

// systemLocale 返回 Windows 当前用户区域设置，如 "zh-CN"。
func systemLocale() string {
	buf := make([]uint16, 85) // LOCALE_NAME_MAX_LENGTH
	n, _, _ := procGetUserDefaultLocaleName.Call(
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)),
	)
	if n == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf[:n])
}
