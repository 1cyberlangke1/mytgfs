//go:build e2e

package main

import "os"

// e2eMockRequested 表示 e2e 构建里要求只起面板与假数据层。
//
// 输入：无
// 输出：MYTGFS_E2E_MOCK=1 时为 true
// 预期：e2e 不连 Telegram，也不等账号鉴权失败
func e2eMockRequested() bool { return os.Getenv("MYTGFS_E2E_MOCK") == "1" }
