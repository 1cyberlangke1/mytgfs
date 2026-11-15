//go:build !e2e

package main

// e2eMockRequested 在正式构建里恒为 false。
//
// 输入：无
// 输出：false
// 预期：假数据分支只在 -tags e2e 时参与编译
func e2eMockRequested() bool { return false }
