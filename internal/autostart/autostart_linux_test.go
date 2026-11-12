//go:build linux

package autostart

// Linux autostart 文件内容单测。

import (
	"strings"
	"testing"
)

func TestDesktopEntry(t *testing.T) {
	body := desktopEntry("/opt/my tgfs/mytgfs")
	checks := []string{
		"[Desktop Entry]",
		"Type=Application",
		"Terminal=false",
		"X-GNOME-Autostart-enabled=true",
		`Exec="/opt/my tgfs/mytgfs" "tray"`,
	}
	for _, c := range checks {
		if !strings.Contains(body, c) {
			t.Errorf("desktop entry missing %q\n%s", c, body)
		}
	}
}

func TestDesktopExecEscaping(t *testing.T) {
	got := desktopExec([]string{`/opt/a"b`, `$HOME/x`, "`tick`"})
	want := `"/opt/a\"b" "\$HOME/x" "` + "\\`tick\\`" + `"`
	if got != want {
		t.Errorf("desktopExec = %s, want %s", got, want)
	}
}
