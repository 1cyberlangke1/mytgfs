//go:build e2e

package trayapp

// E2E 构建专用：打开 WebView2 远程调试端口，并可挂上假数据层。
//
// 输入：环境变量 MYTGFS_E2E_PORT（缺省 9333）、MYTGFS_E2E_MOCK=1
// 输出：传给 WebView2 的启动参数；包装后的面板 handler
// 预期：只在 `go build -tags e2e` 的测试二进制里存在，正式包没有这些开关

import (
	"net/http"
	"os"

	"mytgfs/internal/web"
)

const e2eDefaultPort = "9333"

func e2eBrowserArgs() []string {
	port := os.Getenv("MYTGFS_E2E_PORT")
	if port == "" {
		port = e2eDefaultPort
	}
	return []string{"--remote-debugging-port=" + port}
}

// e2eWrap 在要求假数据时给面板套一层内存实现。
//
// 输入：真面板 handler
// 输出：MYTGFS_E2E_MOCK=1 时为假数据包装，否则原样返回
// 预期：假数据只覆盖卷/文件/下载接口，静态资源与推送仍走真实现；不需要任何账号
func e2eWrap(h http.Handler) http.Handler {
	if os.Getenv("MYTGFS_E2E_MOCK") != "1" {
		return h
	}
	return web.E2EMock(h)
}
