package main

// mytgfs-tray：GUI 子系统入口，双击直接打开桌面窗口与托盘，不弹控制台。
//
// 与 `mytgfs tray` 共用 internal/trayapp；运行期日志统一由 logging.Setup
// 写入文件，不依赖控制台句柄。

import (
	"flag"
	"log/slog"
	"os"

	"mytgfs/internal/logging"
	"mytgfs/internal/trayapp"
)

func main() {
	logging.SetupFromConfig("config.toml")
	fs := flag.NewFlagSet("mytgfs-tray", flag.ExitOnError)
	cfg := fs.String("config", "config.toml", "config file")
	_ = fs.Parse(os.Args[1:])

	if err := trayapp.Run(trayapp.Options{
		ConfigPath:     *cfg,
		SingleInstance: true,
	}); err != nil {
		slog.Error("tray exited", "err", err)
		os.Exit(1)
	}
}
