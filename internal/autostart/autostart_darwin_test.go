//go:build darwin

package autostart

// macOS LaunchAgent 内容单测。

import (
	"strings"
	"testing"
)

func TestPlistContent(t *testing.T) {
	body := plistContent("/Applications/mytgfs.app/Contents/MacOS/mytgfs")
	checks := []string{
		"<key>Label</key>",
		agentLabel,
		"<key>RunAtLoad</key>",
		"<true/>",
		"/Applications/mytgfs.app/Contents/MacOS/mytgfs",
		"<string>tray</string>",
	}
	for _, c := range checks {
		if !strings.Contains(body, c) {
			t.Errorf("plist missing %q\n%s", c, body)
		}
	}
}

func TestXMLEscape(t *testing.T) {
	got := xmlEscape(`a&b<c>"d"'e`)
	want := `a&amp;b&lt;c&gt;&quot;d&quot;&apos;e`
	if got != want {
		t.Errorf("xmlEscape = %s, want %s", got, want)
	}
}
