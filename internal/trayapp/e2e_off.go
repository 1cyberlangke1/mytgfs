//go:build !e2e

package trayapp

// 正式构建：不开放任何浏览器调试参数，也不挂假数据层。

import "net/http"

func e2eBrowserArgs() []string {
	return nil
}

// e2eWrap 在正式构建里原样返回面板 handler。
func e2eWrap(h http.Handler) http.Handler {
	return h
}
