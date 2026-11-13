package config

// [webdav] 段改写的单测：新增、原位更新、清空 volume、保留注释。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readFile 是测试里的小工具。
func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestSetWebDAVAppendsSection 没有 [webdav] 段时追加到末尾。
func TestSetWebDAVAppendsSection(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte("[telegram]\napi_id = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetWebDAV(p, WebDAV{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, p)
	if !strings.Contains(got, "[webdav]") || !strings.Contains(got, "enabled = true") {
		t.Fatalf("配置 = %s", got)
	}
	if !strings.Contains(got, `addr = "127.0.0.1:8080"`) {
		t.Errorf("缺少默认地址: %s", got)
	}
	// 原有段落必须还在
	if !strings.Contains(got, "[telegram]") {
		t.Errorf("原段落被破坏: %s", got)
	}
}

// TestSetWebDAVUpdatesInPlace 已有段落只改受管理的键，注释与未知键保留。
func TestSetWebDAVUpdatesInPlace(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	src := "[webdav]\n# 说明\nenabled = false\naddr = \"127.0.0.1:9999\"\nextra = \"keep\"\n\n[log]\nlevel = \"info\"\n"
	if err := os.WriteFile(p, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetWebDAV(p, WebDAV{Enabled: true, Addr: "127.0.0.1:8080"}); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, p)
	for _, want := range []string{"# 说明", "enabled = true", `addr = "127.0.0.1:8080"`, `extra = "keep"`, "[log]"} {
		if !strings.Contains(got, want) {
			t.Errorf("缺少 %q: %s", want, got)
		}
	}
	if strings.Contains(got, "127.0.0.1:9999") {
		t.Errorf("旧地址未替换: %s", got)
	}
}

// TestSetWebDAVClearsVolume Volume 为空时整行删掉。
func TestSetWebDAVClearsVolume(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte("[webdav]\nvolume = \"demo\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetWebDAV(p, WebDAV{Enabled: false}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(readFile(t, p), "volume") {
		t.Errorf("volume 行未删除: %s", readFile(t, p))
	}
}

// TestWebDAVTargetTarget 卷选择：指定名优先，空则第一个启用的卷。
func TestWebDAVTargetTarget(t *testing.T) {
	vols := []Volume{{Name: "a", Enabled: false}, {Name: "b", Enabled: true}}
	if v, ok := (WebDAV{}).Target(vols); !ok || v.Name != "b" {
		t.Errorf("默认取第一个启用的卷，实际 %v %v", v, ok)
	}
	if v, ok := (WebDAV{Volume: "a"}).Target(vols); !ok || v.Name != "a" {
		t.Errorf("指定卷优先，实际 %v %v", v, ok)
	}
	if _, ok := (WebDAV{Volume: "missing"}).Target(vols); ok {
		t.Error("指定不存在的卷应返回 false")
	}
}
