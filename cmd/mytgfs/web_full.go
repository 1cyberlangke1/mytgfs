//go:build !noweb

package main

// 完整构建（默认）：面板、托盘与命令行都在。
//
// 输入：无
// 输出：常量 false
// 预期：只有 -tags noweb 的构建才把它置为 true
const cliOnlyBuild = false

// dispatchUI 分发面板与托盘命令。
//
// 输入：name 命令名（web / tray）；args 该命令之后的参数
// 输出：true 表示命令已执行；false 表示本构建没有这个命令
// 预期：完整构建不会返回 false
func dispatchUI(name string, args []string) bool {
	switch name {
	case "web":
		runWeb(args)
	case "tray":
		runTray(args)
	default:
		return false
	}
	return true
}
