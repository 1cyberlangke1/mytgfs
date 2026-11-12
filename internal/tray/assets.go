package tray

// 托盘图标资源：Windows 用 ICO（Shell_NotifyIcon 只认图标格式），
// 其它平台用 PNG。两套资源都嵌进二进制，运行时按系统选择。

import (
	"embed"
	"runtime"
)

//go:embed assets/icon.ico assets/icon.png
var assets embed.FS

// iconBytes 返回当前平台可用的图标数据。
//
// 输入：无
// 输出：ICO（Windows）或 PNG（其它平台）字节；资源损坏时返回 nil
// 预期：systray.SetIcon 接受这两种格式，nil 时托盘只显示空白占位
func iconBytes() []byte {
	name := "assets/icon.png"
	if runtime.GOOS == "windows" {
		name = "assets/icon.ico"
	}
	b, err := assets.ReadFile(name)
	if err != nil {
		return nil
	}
	return b
}
