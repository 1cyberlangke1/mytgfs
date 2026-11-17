package main

// mytgfs-tray：GUI 子系统入口，双击直接打开桌面窗口与托盘，不弹控制台。
//
// 与 `mytgfs tray` 共用 internal/trayapp；运行期日志统一由 logging.Setup
// 写入文件，不依赖控制台句柄。

import (
	"flag"
	"log/slog"
	"os"

	"mytgfs/internal/config"
	"mytgfs/internal/logging"
	"mytgfs/internal/tgclient"
	"mytgfs/internal/trayapp"
)

func main() {
	// 安卓上进程 cwd 是只读根目录：先切到 app 私有目录，再碰任何相对路径
	if err := prepareSandbox(); err != nil {
		slog.Error("sandbox unavailable", "err", err)
	}
	if err := prepareE2EConfig(); err != nil {
		slog.Error("e2e config unavailable", "err", err)
	}
	logging.SetupFromConfig(config.DefaultConfigPath)
	fs := flag.NewFlagSet("mytgfs-tray", flag.ExitOnError)
	cfg := fs.String("config", config.DefaultConfigPath, "config file")
	_ = fs.Parse(os.Args[1:])
	if c, err := config.Load(*cfg); err == nil {
		tgclient.SetDefaultProxy(c.Telegram.Proxy)
	}

	if err := trayapp.Run(trayapp.Options{
		ConfigPath:     *cfg,
		SingleInstance: true,
		SetupAllowed:   setupAllowed,
	}); err != nil {
		slog.Error("tray exited", "err", err)
		os.Exit(1)
	}
}
