//go:build e2e

package web

import (
	"net/http"
	"os"
)

// wrapE2E 在 e2e 构建里按环境变量挂上假数据层。
//
// 输入：真面板 handler
// 输出：MYTGFS_E2E_MOCK=1 或 e2eandroid 构建时为假数据包装，否则原样返回
// 预期：网页版与桌面壳共用这一处开关；安卓没有传环境变量的途径，
//
//	由 -tags e2eandroid 在编译期打开，正式二进制没有这个分支
func wrapE2E(h http.Handler) http.Handler {
	if os.Getenv("MYTGFS_E2E_MOCK") != "1" && !forcedE2E() {
		return h
	}
	return E2EMock(h)
}
