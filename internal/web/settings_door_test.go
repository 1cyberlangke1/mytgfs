package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mytgfs/internal/config"
)

// 回归背景：设置保存曾绕过 App 的配置门直接写盘，
// 内存副本与文件脱节，运行中的卷吃不到新的并发参数。
// 现在设置 PUT 必须经 MutateConfig 收敛，并触发 OnSettingsChanged 广播。
func TestSettingsPUTGoesThroughMutateConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	base := "[mount]\nsync_seconds = 15\n[log]\nlevel = \"info\"\n"
	if err := os.WriteFile(path, []byte(base), 0o600); err != nil {
		t.Fatal(err)
	}

	var notified config.Config
	hookFired := false
	opts := Options{
		MutateConfig: func(mutate func(string) error) error {
			if err := mutate(path); err != nil {
				return err
			}
			cur, err := config.Load(path)
			if err != nil {
				return err
			}
			notified = cur
			hookFired = true
			return nil
		},
	}
	h := handleAPISettings(path, opts)

	// PUT 经门写入：文件与回调同时拿到新值
	body := `{"values":{"mount.sync_seconds":17,"log.level":"debug"}}`
	req := httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if !hookFired {
		t.Fatal("保存后 OnSettingsChanged 未触发")
	}
	if got := notified.Mount.SyncSeconds; got != 17 {
		t.Fatalf("回调配置 sync_seconds = %d; want 17", got)
	}
	if got := notified.Log.Level; got != "debug" {
		t.Fatalf("回调配置 log.level = %q; want debug", got)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "17") {
		t.Fatalf("配置文件里没有 17:\n%s", raw)
	}
}

// GET 保持原语义：返回 schema 与当前值
func TestSettingsGETReturnsSchemaAndValues(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("[mount]\nsync_seconds = 15\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := handleAPISettings(path, Options{})
	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d", rec.Code)
	}
	var out struct {
		Schema []struct{ Key string } `json:"schema"`
		Values map[string]string      `json:"values"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Schema) == 0 {
		t.Fatal("schema 为空")
	}
	if out.Values["mount.sync_seconds"] != "15" {
		t.Fatalf("mount.sync_seconds = %q; want 15", out.Values["mount.sync_seconds"])
	}
}
