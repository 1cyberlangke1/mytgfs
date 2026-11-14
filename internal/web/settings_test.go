package web

// 设置接口单测：缺省键必须返回运行时真实默认值，不能是 0 或空串。

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"mytgfs/internal/config"
)

// settingsPayload 是 GET /api/settings 的响应体。
type settingsPayload struct {
	Schema []config.SettingInfo `json:"schema"`
	Values map[string]string    `json:"values"`
}

// TestSettingsAPIUsesRuntimeDefaults 配置里没写的键返回真实默认值。
//
// 输入：配置文件只写 log.level=debug
// 输出：mount.* / downloads.dir / web.port 都是 config 包的默认常量
// 预期：面板不再显示 0、也不出现空串
func TestSettingsAPIUsesRuntimeDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[log]\nlevel = \"debug\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	handleAPISettings(path)(rec, httptest.NewRequest(http.MethodGet, "/api/settings", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got settingsPayload
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	want := map[string]string{
		"web.port":                strconv.Itoa(config.DefaultWebPort),
		"downloads.dir":           config.DefaultDir,
		"mount.sync_seconds":      strconv.Itoa(config.DefaultSyncSeconds),
		"mount.download_parallel": strconv.Itoa(config.DefaultDownloadParallel),
		"mount.read_chunk_mb":     strconv.Itoa(config.DefaultReadChunkMB),
		"mount.upload_parallel":   strconv.Itoa(config.DefaultUploadParallel),
		"log.level":               "debug",
	}
	for key, v := range want {
		if got.Values[key] != v {
			t.Errorf("values[%q] = %q，期望 %q", key, got.Values[key], v)
		}
	}
	// 每个 schema 键都要有值，且不允许出现占位 0
	for _, s := range got.Schema {
		if strings.TrimSpace(got.Values[s.Key]) == "" {
			t.Errorf("键 %q 返回空值", s.Key)
		}
	}
}

// TestSettingsAPIRejectsUnknownKey 未知键返回 400，不静默写入。
func TestSettingsAPIRejectsUnknownKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	body := strings.NewReader(`{"values":{"nope.key":"1"}}`)
	rec := httptest.NewRecorder()
	handleAPISettings(path)(rec, httptest.NewRequest(http.MethodPut, "/api/settings", body))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, 期望 400", rec.Code)
	}
}
