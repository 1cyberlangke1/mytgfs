//go:build !e2e

package web

import "net/http"

// wrapE2E 在正式构建里原样返回 handler。
//
// 输入：真面板 handler
// 输出：同一个 handler
// 预期：假数据层只在 -tags e2e 时参与编译
func wrapE2E(h http.Handler) http.Handler { return h }
