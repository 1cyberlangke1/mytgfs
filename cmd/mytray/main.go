package main

// mytgfs-tray：GUI 子系统入口，双击直接打开桌面窗口与托盘，不弹控制台。
//
// 与 `mytgfs tray` 共用 internal/trayapp；启动失败写 data/tray-error.log，
// 因为没有控制台可以打印。

import (
	"flag"
	"fmt"
	"os"

	"mytgfs/internal/trayapp"
)

func main() {
	fs := flag.NewFlagSet("mytgfs-tray", flag.ExitOnError)
	point := fs.String("point", "", "mount point")
	cfg := fs.String("config", "config.toml", "config file")
	_ = fs.Parse(os.Args[1:])

	if err := trayapp.Run(trayapp.Options{
		ConfigPath: *cfg,
		Point:      *point,
	}); err != nil {
		writeTrayError(err.Error())
		os.Exit(1)
	}
}

// writeTrayError 把启动错误追加到 data/tray-error.log。
func writeTrayError(msg string) {
	_ = os.MkdirAll("data", 0o755)
	f, err := os.OpenFile("data/tray-error.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s\n", msg)
}
