package main

import (
	"flag"

	"mytgfs/internal/i18n"
	"mytgfs/internal/trayapp"
)

// runTray 启动桌面壳与托盘（Wails 窗口 + 系统托盘）。
//
// 输入：args 子命令参数
// 输出：无；失败直接退出
// 预期：不监听端口，面板由内嵌 webview 承载
func runTray(args []string) {
	fs := flag.NewFlagSet("tray", flag.ExitOnError)
	point := fs.String("point", "", i18n.T("cli.flag.mount_point"))
	_ = fs.Parse(args)

	if err := trayapp.Run(trayapp.Options{
		ConfigPath: "config.toml",
		Point:      *point,
	}); err != nil {
		fatal(err.Error())
	}
}
