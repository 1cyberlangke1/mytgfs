package main

// 命令行纯逻辑的单测：路径规范化、卷查找、参数处理。

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"mytgfs/internal/config"
	"mytgfs/internal/ui"
)

// TestChatID 频道 ID 转成 -100 前缀的聊天 ID。
func TestChatID(t *testing.T) {
	cases := map[int64]int64{
		1234567890: -1001234567890,
		1:          -1000000000001,
	}
	for in, want := range cases {
		if got := chatID(in); got != want {
			t.Errorf("chatID(%d) = %d，期望 %d", in, got, want)
		}
	}
}

// TestVolPath 命令行路径统一成正斜杠、带前导斜杠并被 Clean。
func TestVolPath(t *testing.T) {
	cases := map[string]string{
		"":             "/",
		"a.txt":        "/a.txt",
		`sub\a.txt`:    "/sub/a.txt",
		"/sub/./a.txt": "/sub/a.txt",
		"sub//b.txt":   "/sub/b.txt",
		"/../a.txt":    "/a.txt",
	}
	for in, want := range cases {
		if got := volPath(in); got != want {
			t.Errorf("volPath(%q) = %q，期望 %q", in, got, want)
		}
	}
}

// TestRemoteDir 远程目录与命令行路径同样的规范化规则。
func TestRemoteDir(t *testing.T) {
	cases := map[string]string{
		"":           "/",
		"batch":      "/batch",
		`batch\sub`:  "/batch/sub",
		"/batch/./x": "/batch/x",
	}
	for in, want := range cases {
		if got := remoteDir(in); got != want {
			t.Errorf("remoteDir(%q) = %q，期望 %q", in, got, want)
		}
	}
}

// TestVolumeByName 有 volumes 时按名字查，没有时退化成默认频道。
func TestVolumeByName(t *testing.T) {
	withVols := config.Config{Volumes: []config.Volume{
		{Name: "demo", ChatID: 11},
		{Name: "demo2", ChatID: 22},
	}}
	if v, ok := volumeByName(withVols, "demo2"); !ok || v.ChatID != 22 {
		t.Fatalf("demo2 解析失败: %+v ok=%v", v, ok)
	}
	if _, ok := volumeByName(withVols, "nope"); ok {
		t.Fatal("未知名不应命中")
	}

	legacy := config.Config{Telegram: config.Telegram{ChannelID: 99}}
	if v, ok := volumeByName(legacy, "mytgfs"); !ok || v.ChatID != 99 {
		t.Fatalf("默认卷解析失败: %+v ok=%v", v, ok)
	}
	if _, ok := volumeByName(legacy, "demo"); ok {
		t.Fatal("无 volumes 时只应有 mytgfs")
	}
}

// TestStripJSONFlag 取出 --json，其余参数原样保序。
func TestStripJSONFlag(t *testing.T) {
	got := stripJSONFlag([]string{"volume", "--json", "check", "--name", "demo"})
	want := []string{"volume", "check", "--name", "demo"}
	if !slices.Equal(got, want) {
		t.Fatalf("参数 = %v，期望 %v", got, want)
	}
	if !ui.JSONMode() {
		t.Fatal("--json 应打开 JSON 模式")
	}
}

// TestReadTrim 读文件并去掉首尾空白；缺文件返回空串。
func TestReadTrim(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a.txt")
	if err := os.WriteFile(p, []byte("  hello \r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := readTrim(p); got != "hello" {
		t.Fatalf("readTrim = %q，期望 hello", got)
	}
	if got := readTrim(filepath.Join(t.TempDir(), "absent")); got != "" {
		t.Fatalf("缺文件应返回空串，得到 %q", got)
	}
}
