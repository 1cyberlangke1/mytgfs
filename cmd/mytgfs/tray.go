package main

// `mytgfs tray` 子命令：转交给托盘主管进程实现。

import (
	"flag"

	"mytgfs/internal/trayapp"
)

// runTray 启动系统托盘常驻进程。
//
// 输入：--port 面板端口；--point 挂载点覆盖
// 输出：无；启动失败时退出
// 预期：挂载、自动同步、按需面板都由 trayapp 负责
func runTray(args []string) {
	fs := flag.NewFlagSet("tray", flag.ExitOnError)
	port := fs.Int("port", 8720, "web panel port")
	point := fs.String("point", "", "mount point")
	_ = fs.Parse(args)

	if err := trayapp.Run(trayapp.Options{
		ConfigPath: "config.toml",
		Point:      *point,
		Port:       *port,
	}); err != nil {
		fatal(err.Error())
	}
}
