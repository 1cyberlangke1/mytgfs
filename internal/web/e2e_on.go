//go:build e2e

package web

import (
	"net/http"
	"os"
)

// wrapE2E 在 e2e 构建里按环境变量挂上假数据层。
//
// 输入：真面板 handler
// 输出：MYTGFS_E2E_MOCK=1 时为假数据包装，否则原样返回
// 预期：网页版与桌面壳共用这一处开关，正式二进制没有这个分支
func wrapE2E(h http.Handler) http.Handler {
	if os.Getenv("MYTGFS_E2E_MOCK") != "1" {
		return h
	}
	return E2EMock(h)
}
