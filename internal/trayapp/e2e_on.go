//go:build e2e

package trayapp

// E2E 构建专用：打开 WebView2 远程调试端口。
//
// 输入：环境变量 MYTGFS_E2E_PORT（缺省 9333）
// 输出：传给 WebView2 的启动参数
// 预期：只在 `go build -tags e2e` 的测试二进制里存在，正式包没有这个开关；
//       假数据层在 internal/web 的构建标签里统一挂载，桌面壳与网页版共用

import "os"

const e2eDefaultPort = "9333"

func e2eBrowserArgs() []string {
	port := os.Getenv("MYTGFS_E2E_PORT")
	if port == "" {
		port = e2eDefaultPort
	}
	return []string{"--remote-debugging-port=" + port}
}
