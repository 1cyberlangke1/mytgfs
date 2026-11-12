//go:build !windows

package i18n

// 非 Windows 平台：按惯例读 LANG / LC_* 环境变量。

import "os"

// systemLocale 返回 POSIX 风格的语言环境，如 "zh_CN.UTF-8"。
func systemLocale() string {
	for _, k := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}
