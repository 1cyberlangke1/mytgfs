//go:build !e2e

package main

// prepareE2EConfig 在正式构建里什么都不做。
//
// 输入：无
// 输出：nil
// 预期：最小配置只存在于 -tags e2e 的壳里
func prepareE2EConfig() error { return nil }
