//go:build darwin

package autostart

// macOS 开机自启：LaunchAgent（~/Library/LaunchAgents/com.mytgfs.tray.plist）。

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/natefinch/atomic"
)

const agentLabel = "com.mytgfs.tray"

// plistPath 返回当前用户的 LaunchAgent 路径。
func plistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", agentLabel+".plist"), nil
}

// Enable 写入 LaunchAgent plist，下次登录自动加载。
//
// 输入：exe 可执行文件绝对路径
// 输出：error
// 预期：RunAtLoad=true；不设置 KeepAlive，用户主动退出后不复活；同目录有\n//       mytgfs-tray 时优先注册它
func Enable(exe string) error {
	exe = PreferTray(exe)
	p, err := plistPath()
	if err != nil {
		return err
	}
	return atomic.WriteFile(p, strings.NewReader(plistContent(exe)))
}

// Disable 删除 LaunchAgent plist。
//
// 输入：无
// 输出：error
// 预期：文件不存在视为成功；已加载的本次会话进程不受影响
func Disable() error {
	p, err := plistPath()
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Enabled 判断 LaunchAgent plist 是否存在。
func Enabled() (bool, error) {
	p, err := plistPath()
	if err != nil {
		return false, err
	}
	if _, err := os.Stat(p); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// plistContent 生成 LaunchAgent 配置。
//
// 输入：exe 可执行文件路径
// 输出：plist XML
// 预期：ProgramArguments 与 Command 一致，RunAtLoad 启动一次
func plistContent(exe string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	b.WriteString("<plist version=\"1.0\">\n<dict>\n")
	fmt.Fprintf(&b, "  <key>Label</key>\n  <string>%s</string>\n", agentLabel)
	b.WriteString("  <key>ProgramArguments</key>\n  <array>\n")
	for _, a := range Command(exe) {
		fmt.Fprintf(&b, "    <string>%s</string>\n", xmlEscape(a))
	}
	b.WriteString("  </array>\n")
	b.WriteString("  <key>RunAtLoad</key>\n  <true/>\n")
	b.WriteString("</dict>\n</plist>\n")
	return b.String()
}

// xmlEscape 转义 plist 里的 XML 特殊字符。
func xmlEscape(s string) string {
	return strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&apos;",
	).Replace(s)
}
