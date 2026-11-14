package config

// 配置追加卷的单测：保留原内容、重名拒绝、末尾写入可解析。

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func writeTempConfig(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestAppendVolume(t *testing.T) {
	p := writeTempConfig(t, "[log]\nlevel = \"debug\"\n")

	if err := AppendVolume(p, Volume{Name: "work", ChatID: 111, Enabled: true}); err != nil {
		t.Fatalf("AppendVolume: %v", err)
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Log.Level != "debug" {
		t.Errorf("existing config lost: level = %q", cfg.Log.Level)
	}
	if len(cfg.Volumes) != 1 {
		t.Fatalf("volumes = %d", len(cfg.Volumes))
	}
	v := cfg.Volumes[0]
	if v.Name != "work" || v.ChatID != 111 || !v.Enabled {
		t.Errorf("volume = %+v", v)
	}

	// 第二个卷
	if err := AppendVolume(p, Volume{Name: "media", ChatID: 222, Enabled: true}); err != nil {
		t.Fatalf("AppendVolume 2: %v", err)
	}
	cfg, err = Load(p)
	if err != nil {
		t.Fatalf("Load 2: %v", err)
	}
	if len(cfg.Volumes) != 2 {
		t.Fatalf("volumes = %d", len(cfg.Volumes))
	}
}

func TestAppendVolumeRejectsDuplicate(t *testing.T) {
	p := writeTempConfig(t, "[telegram]\napi_id = 1\n")
	if err := AppendVolume(p, Volume{Name: "work", ChatID: 111}); err != nil {
		t.Fatal(err)
	}
	err := AppendVolume(p, Volume{Name: "WORK", ChatID: 999})
	if !errors.Is(err, ErrVolumeExists) {
		t.Fatalf("err = %v, want ErrVolumeExists", err)
	}
}

func TestAppendVolumeValidatesName(t *testing.T) {
	p := writeTempConfig(t, "")
	if err := AppendVolume(p, Volume{Name: "  "}); err == nil {
		t.Error("empty name should fail")
	}
	if err := AppendVolume(p, Volume{Name: "a\nb"}); err == nil {
		t.Error("newline in name should fail")
	}
}

func TestAppendVolumeWithoutTrailingNewline(t *testing.T) {
	p := writeTempConfig(t, "[log]\nlevel = \"info\"")
	if err := AppendVolume(p, Volume{Name: "work", ChatID: 111}); err != nil {
		t.Fatalf("AppendVolume: %v", err)
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Volumes) != 1 || cfg.Volumes[0].Name != "work" {
		t.Errorf("volumes = %+v", cfg.Volumes)
	}
}

func TestTomlStringEscapes(t *testing.T) {
	if got := tomlString(`a\b"c`); got != `"a\\b\"c"` {
		t.Errorf("tomlString = %s", got)
	}
}
