//go:build !android

package main

// 桌面启动准备：工作目录就是用户启动程序的位置，不做切换。
//
// 输入：无
// 输出：恒为 nil
// 预期：与安卓共用同一个 main，桌面行为保持不变

func prepareSandbox() error { return nil }

// setupAllowed 为 false：缺凭证时按原样报错，由命令行 auth 先把凭证补齐。
const setupAllowed = false
