//go:build noweb

package main

// CLI-only 构建（-tags noweb）：只保留命令行，不链接面板与托盘。
//
// 输入：无
// 输出：dispatchUI 恒返回 false；cliOnlyBuild 为 true
// 预期：internal/web、internal/trayapp 与 Wails 都不参与编译，且 web/tray 命令完全不存在

// cliOnlyBuild 表示当前二进制是用 -tags noweb 编译的纯命令行版本。
const cliOnlyBuild = true

// dispatchUI 在 CLI-only 构建里没有可分发的界面命令。
//
// 输入：name 命令名；args 该命令之后的参数（都忽略）
// 输出：恒为 false
// 预期：调用方落到 usage 分支，与输入未知命令完全一致（exit 2）
func dispatchUI(_ string, _ []string) bool {
	return false
}
