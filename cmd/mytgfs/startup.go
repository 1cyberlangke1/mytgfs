package main

// `mytgfs startup` 子命令：开机自启开关，默认关闭。
//
// 开与关直接改系统里的注册项（Windows Run 键 / Linux .desktop / macOS LaunchAgent），
// 状态以系统为准，配置文件不重复保存。

import (
	"fmt"
	"os"

	"mytgfs/internal/autostart"
	"mytgfs/internal/i18n"
)

// runStartup 处理 enable / disable / status。
//
// 输入：args 子命令
// 输出：打印结果；失败退出 1
// 预期：未支持平台返回明确错误，不静默成功
func runStartup(args []string) {
	if len(args) == 0 {
		usage()
		os.Exit(2)
	}
	exe, err := os.Executable()
	if err != nil {
		fatal(fmt.Sprintf("%s: %v", i18n.T("startup.failed"), err))
	}
	switch args[0] {
	case "enable":
		if err := autostart.Enable(exe); err != nil {
			fatal(fmt.Sprintf("%s: %v", i18n.T("startup.failed"), err))
		}
		fmt.Println(i18n.T("startup.enabled"))
	case "disable":
		if err := autostart.Disable(); err != nil {
			fatal(fmt.Sprintf("%s: %v", i18n.T("startup.failed"), err))
		}
		fmt.Println(i18n.T("startup.disabled"))
	case "status":
		on, err := autostart.Enabled()
		if err != nil {
			fatal(fmt.Sprintf("%s: %v", i18n.T("startup.failed"), err))
		}
		if on {
			fmt.Println(i18n.T("startup.on"))
		} else {
			fmt.Println(i18n.T("startup.off"))
		}
	default:
		usage()
		os.Exit(2)
	}
}
