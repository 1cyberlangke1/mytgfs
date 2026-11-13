package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mytgfs/internal/config"
	"mytgfs/internal/version"
)

func TestPWADataFromConfig(t *testing.T) {
	off := false
	d := newPWAData(config.PWA{Enabled: &off, Name: "X", OfflineMaxMB: 10})
	if d.Enabled || d.Name != "X" || d.OfflineMax != 10<<20 {
		t.Fatalf("unexpected pwaData: %+v", d)
	}
	d = newPWAData(config.PWA{})
	if !d.Enabled || d.Name != config.DefaultPWAName {
		t.Fatalf("defaults not applied: %+v", d)
	}
}

func TestHandleManifest(t *testing.T) {
	w := httptest.NewRecorder()
	handleManifest(config.PWA{Name: "Drive"})(w, httptest.NewRequest(http.MethodGet, "/manifest.webmanifest", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/manifest+json") {
		t.Fatalf("content-type=%q", ct)
	}
	var m webManifest
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if m.Name != "Drive" || m.ShortName != "Drive" || m.Display != "standalone" || m.StartURL != "/" {
		t.Fatalf("bad manifest: %+v", m)
	}
	if len(m.Icons) != 3 {
		t.Fatalf("icons=%d", len(m.Icons))
	}
	if m.Icons[2].Purpose != "maskable" {
		t.Fatalf("last icon purpose=%q", m.Icons[2].Purpose)
	}
}

func TestHandleManifestDisabled(t *testing.T) {
	off := false
	w := httptest.NewRecorder()
	handleManifest(config.PWA{Enabled: &off})(w, httptest.NewRequest(http.MethodGet, "/manifest.webmanifest", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404", w.Code)
	}
}

func TestHandleServiceWorker(t *testing.T) {
	w := httptest.NewRecorder()
	handleServiceWorker()(w, httptest.NewRequest(http.MethodGet, "/sw.js", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}
	body := w.Body.String()
	if strings.Contains(body, "__VERSION__") {
		t.Fatal("version placeholder not replaced")
	}
	if body == "" || !strings.Contains(body, "SHELL_CACHE") {
		t.Fatal("sw body looks wrong")
	}
	// 版本号必须进缓存名：发布新版会换掉旧缓存
	ver := version.Version
	if ver == "" {
		ver = "dev"
	}
	if !strings.Contains(body, ver) {
		t.Fatalf("version %q not injected", ver)
	}
}

// TestMuxPWARoutes 验证开关真正控制 PWA 路由，而不只是页面文案。
func TestMuxPWARoutes(t *testing.T) {
	paths := []string{"/manifest.webmanifest", "/sw.js", "/static/icon-192.png", "/favicon.ico"}
	off := false

	onMux := newMux("", Options{PWA: config.PWA{}})
	for _, p := range paths {
		w := httptest.NewRecorder()
		onMux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, p, nil))
		if w.Code != http.StatusOK {
			t.Errorf("enabled %s => %d want 200", p, w.Code)
		}
	}

	offMux := newMux("", Options{PWA: config.PWA{Enabled: &off}})
	for _, p := range paths {
		w := httptest.NewRecorder()
		offMux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, p, nil))
		if w.Code != http.StatusNotFound {
			t.Errorf("disabled %s => %d want 404", p, w.Code)
		}
	}
}

func TestIconsEmbedded(t *testing.T) {
	if len(icon192) == 0 || len(icon512) == 0 || len(appleTouchIcon) == 0 {
		t.Fatal("one of the embedded icons is empty")
	}
	// PNG 魔数
	sig := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	for _, b := range [][]byte{icon192, icon512, appleTouchIcon} {
		if len(b) < 8 || string(b[:8]) != string(sig) {
			t.Fatal("icon is not a PNG")
		}
	}
}
