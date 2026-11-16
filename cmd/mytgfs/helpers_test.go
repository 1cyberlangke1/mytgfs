package main

// 命令行纯逻辑的单测：路径规范化、卷查找、参数处理。

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"mytgfs/internal/config"
	"mytgfs/internal/fm"
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

// TestLsEntries 目录项转 JSON 时补上卷内完整路径。
//
// 输入：根目录与子目录两种 dir，混合文件/目录项
// 输出：Path 为 dir+name，其余字段原样透传
// 预期：根目录不产生双斜杠；空列表返回非 nil 空切片
func TestLsEntries(t *testing.T) {
	mod := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	got := lsEntries("/", []fm.Entry{
		{Name: "a.txt", Size: 3, Mod: mod},
		{Name: "sub", Dir: true},
	})
	if len(got) != 2 {
		t.Fatalf("条目数 = %d，期望 2", len(got))
	}
	if got[0].Path != "/a.txt" || got[0].Size != 3 || !got[0].Mod.Equal(mod) {
		t.Fatalf("文件条目 = %+v，期望 path=/a.txt size=3", got[0])
	}
	if got[1].Path != "/sub" || !got[1].Dir {
		t.Fatalf("目录条目 = %+v，期望 path=/sub dir=true", got[1])
	}

	sub := lsEntries("/batch", []fm.Entry{{Name: "b.bin"}})
	if len(sub) != 1 || sub[0].Path != "/batch/b.bin" {
		t.Fatalf("子目录路径 = %+v，期望 /batch/b.bin", sub)
	}

	empty := lsEntries("/", nil)
	if empty == nil || len(empty) != 0 {
		t.Fatalf("空列表应返回非 nil 空切片，得到 %#v", empty)
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
