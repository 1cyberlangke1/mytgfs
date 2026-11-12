package autostart

// 跨平台共用的命令拼接单测。

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCommand(t *testing.T) {
	exe := filepath.Join(string(filepath.Separator), "opt", "mytgfs", "mytgfs")
	cases := []struct {
		exe  string
		want []string
	}{
		{exe, []string{exe, "tray"}},
		{exe + "-tray", []string{exe + "-tray"}},
		{"C:\\Program Files\\mytgfs\\mytgfs-tray.exe", []string{"C:\\Program Files\\mytgfs\\mytgfs-tray.exe"}},
	}
	for _, c := range cases {
		got := Command(c.exe)
		if len(got) != len(c.want) {
			t.Fatalf("Command(%q) = %v, want %v", c.exe, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("Command(%q)[%d] = %q, want %q", c.exe, i, got[i], c.want[i])
			}
		}
	}
}

func TestPreferTray(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "mytgfs")
	if got := PreferTray(exe); got != exe {
		t.Errorf("PreferTray without tray binary = %q, want %q", got, exe)
	}

	tray := filepath.Join(dir, trayName())
	if err := os.WriteFile(tray, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := PreferTray(exe); got != tray {
		t.Errorf("PreferTray = %q, want %q", got, tray)
	}
}
