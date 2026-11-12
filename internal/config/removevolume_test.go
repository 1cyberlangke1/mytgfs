package config

// 删卷：从配置里移除 [[volumes]] 段落。

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeConfig 写一份临时配置文件并返回路径。
func writeConfig(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return p
}

func TestRemoveVolume(t *testing.T) {
	body := `# 顶部注释

[telegram]
api_id = 1

[[volumes]]
name = "work"
chat_id = 111
point = "Y:"
enabled = true

[[volumes]]
name = "media"
chat_id = 222
enabled = true
`
	p := writeConfig(t, body)

	if err := RemoveVolume(p, "work"); err != nil {
		t.Fatalf("RemoveVolume: %v", err)
	}

	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	s := string(got)
	if strings.Contains(s, `name = "work"`) {
		t.Errorf("work 段没删掉:\n%s", s)
	}
	if !strings.Contains(s, `name = "media"`) {
		t.Errorf("media 段被误删:\n%s", s)
	}
	if !strings.Contains(s, "# 顶部注释") {
		t.Errorf("注释丢失:\n%s", s)
	}

	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Volumes) != 1 || cfg.Volumes[0].Name != "media" {
		t.Errorf("解析结果 = %+v", cfg.Volumes)
	}
}

func TestRemoveVolumeCaseInsensitive(t *testing.T) {
	p := writeConfig(t, "[[volumes]]\nname = \"Work\"\nchat_id = 1\nenabled = true\n")
	if err := RemoveVolume(p, "work"); err != nil {
		t.Fatalf("RemoveVolume: %v", err)
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Volumes) != 0 {
		t.Errorf("卷没删掉: %+v", cfg.Volumes)
	}
}

func TestRemoveVolumeNotFound(t *testing.T) {
	p := writeConfig(t, "[[volumes]]\nname = \"work\"\nchat_id = 1\nenabled = true\n")
	err := RemoveVolume(p, "nope")
	if !errors.Is(err, ErrVolumeNotFound) {
		t.Fatalf("err = %v, want ErrVolumeNotFound", err)
	}
}

func TestRemoveVolumeEmptyName(t *testing.T) {
	p := writeConfig(t, "")
	if err := RemoveVolume(p, "  "); err == nil {
		t.Fatal("空卷名应该失败")
	}
}
