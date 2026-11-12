package autostart

// 开机自启：注册/注销当前可执行文件，默认关闭。
//
// 平台差异由同目录的构建标签文件实现：
//   - Windows：HKCU\Software\Microsoft\Windows\CurrentVersion\Run
//   - Linux：~/.config/autostart/mytgfs.desktop（XDG Autostart）
//   - macOS：~/Library/LaunchAgents/com.mytgfs.tray.plist（LaunchAgent）
//
// 是否开启以系统里的注册状态为唯一事实来源，不在配置里重复保存。

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ErrUnsupported 表示当前平台不支持开机自启。
var ErrUnsupported = errors.New("autostart is not supported on this platform")

// trayName 是 GUI 托盘二进制的文件名。
//
// 输入：无
// 输出：Windows 带 .exe，其它平台不带
// 预期：与 build.ps1 产出的 mytgfs-tray 对应
func trayName() string {
	if runtime.GOOS == "windows" {
		return "mytgfs-tray.exe"
	}
	return "mytgfs-tray"
}

// PreferTray 同目录存在托盘二进制时改用它。
//
// 输入：exe 当前可执行文件路径
// 输出：托盘二进制路径；不存在时原样返回 exe
// 预期：开机能直接进托盘，不弹控制台窗口
func PreferTray(exe string) string {
	p := filepath.Join(filepath.Dir(exe), trayName())
	if st, err := os.Stat(p); err == nil && !st.IsDir() {
		return p
	}
	return exe
}

// Command 返回登录系统时要启动的命令。
//
// 输入：exe 当前可执行文件的绝对路径
// 输出：参数切片，首项是 exe
// 预期：GUI 版（mytgfs-tray.exe / 名字含 tray）无参数直接进托盘；
// 命令行版需要显式追加 tray 子命令
func Command(exe string) []string {
	base := strings.ToLower(filepath.Base(exe))
	if strings.Contains(base, "tray") {
		return []string{exe}
	}
	return []string{exe, "tray"}
}
