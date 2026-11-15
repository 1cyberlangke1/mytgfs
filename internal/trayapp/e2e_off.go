//go:build !e2e

package trayapp

// 正式构建：不开放任何浏览器调试参数。

func e2eBrowserArgs() []string {
	return nil
}
