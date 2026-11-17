//go:build !android

package trayapp

// 桌面没有后台保活需求：托盘常驻进程本身就是保活机制。

func startForeground() {}
