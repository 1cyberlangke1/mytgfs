package trayapp

// 托盘主管进程：挂载卷、自动增量同步、按需开关 Web 面板、托盘菜单。
//
// 这个包不依赖命令行参数解析，方便 CLI 的 `tray` 子命令和
// GUI 子系统的 `mytgfs-tray.exe` 共用同一套逻辑。

import (
	"fmt"
	"net/url"
)

// volumeMenuLabel 返回托盘卷入口的文字。
//
// 输入：name 卷名；used 已用字节（<0 表示读库失败，容量未知）
// 输出：形如 "demo · 159.1 MB"；容量未知时只有卷名
// 预期：托盘一眼看清是哪个盘、占多少；绝不把读取失败显示成 0 B
func volumeMenuLabel(name string, used int64) string {
	if used < 0 {
		return name
	}
	return fmt.Sprintf("%s · %s", name, humanBytes(used))
}

// volumeURL 返回面板按卷快速打开的地址。
//
// 输入：name 卷名
// 输出：带 tab/vol 查询参数的站内路径
// 预期：前端首屏读参数直接切到该卷文件页；卷名做 URL 转义
func volumeURL(name string) string {
	return "/?tab=files&vol=" + url.QueryEscape(name)
}

// humanBytes 把字节数格式化成人读文本。
//
// 输入：n 字节数
// 输出：带单位的两段式字符串
// 预期：按 1024 进位，单位最大到 TB
func humanBytes(n int64) string {
	units := []string{"B", "KB", "MB", "GB", "TB"}
	f := float64(n)
	i := 0
	for f >= 1024 && i < len(units)-1 {
		f /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%d %s", n, units[i])
	}
	return fmt.Sprintf("%.1f %s", f, units[i])
}

// stateCode 把托盘状态语言包键映射成机器可读代码。
//
// 输入：key 形如 tray.state.connected
// 输出：online / connecting / reconnecting / auth_lost / starting
// 预期：面板按代码决定状态条颜色，不再匹配翻译后的文本

func stateCode(key string) string {
	switch key {
	case "tray.state.connected":
		return "online"
	case "tray.state.connecting":
		return "connecting"
	case "tray.state.reconnecting":
		return "reconnecting"
	case "tray.state.auth_lost":
		return "auth_lost"
	default:
		return "starting"
	}
}
