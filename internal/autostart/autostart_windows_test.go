//go:build windows

package autostart

// Windows 启动命令行拼接单测。

import "testing"

func TestCommandLine(t *testing.T) {
	cases := []struct {
		exe  string
		want string
	}{
		{`C:\mytgfs\mytgfs.exe`, `C:\mytgfs\mytgfs.exe tray`},
		{`C:\Program Files\mytgfs\mytgfs-tray.exe`, `"C:\Program Files\mytgfs\mytgfs-tray.exe"`},
		{`C:\Program Files\mytgfs\mytgfs.exe`, `"C:\Program Files\mytgfs\mytgfs.exe" tray`},
	}
	for _, c := range cases {
		if got := commandLine(c.exe); got != c.want {
			t.Errorf("commandLine(%q) = %q, want %q", c.exe, got, c.want)
		}
	}
}
