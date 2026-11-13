package web

// 开机自启接口的单元测试：用假控制替代系统注册项。

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeAutostart 记录写入调用并回放固定状态。
type fakeAutostart struct {
	on      bool
	setErr  error
	readErr error
	calls   []bool
}

func (f *fakeAutostart) Enabled() (bool, error) {
	if f.readErr != nil {
		return false, f.readErr
	}
	return f.on, nil
}

func (f *fakeAutostart) Set(on bool) error {
	f.calls = append(f.calls, on)
	if f.setErr != nil {
		return f.setErr
	}
	f.on = on
	return nil
}

// decodeEnabled 解析 {"enabled":bool}。
func decodeEnabled(t *testing.T, body string) bool {
	t.Helper()
	var out struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("响应不是合法 JSON: %v (%s)", err, body)
	}
	return out.Enabled
}

// TestAutostartGet 读状态直接转发系统值。
func TestAutostartGet(t *testing.T) {
	ctl := &fakeAutostart{on: true}
	rec := httptest.NewRecorder()
	handleAPIAutostart(Options{Autostart: ctl}).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/autostart", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d, 期望 200", rec.Code)
	}
	if !decodeEnabled(t, rec.Body.String()) {
		t.Fatal("期望 enabled=true")
	}
	if len(ctl.calls) != 0 {
		t.Fatalf("读操作不应写注册项, 实际调用 %v", ctl.calls)
	}
}

// TestAutostartPost 开关写入后回显新状态。
func TestAutostartPost(t *testing.T) {
	ctl := &fakeAutostart{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/autostart", strings.NewReader(`{"enabled":true}`))
	handleAPIAutostart(Options{Autostart: ctl}).
		ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d, 期望 200 (%s)", rec.Code, rec.Body.String())
	}
	if len(ctl.calls) != 1 || ctl.calls[0] != true {
		t.Fatalf("写入调用 = %v, 期望 [true]", ctl.calls)
	}
	if !decodeEnabled(t, rec.Body.String()) {
		t.Fatal("期望回显 enabled=true")
	}
}

// TestAutostartUnavailable 未注入控制时回 501。
func TestAutostartUnavailable(t *testing.T) {
	rec := httptest.NewRecorder()
	handleAPIAutostart(Options{}).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/autostart", nil))
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("状态码 = %d, 期望 501", rec.Code)
	}
}

// TestAutostartMethodNotAllowed 只接受 GET/POST。
func TestAutostartMethodNotAllowed(t *testing.T) {
	rec := httptest.NewRecorder()
	handleAPIAutostart(Options{Autostart: &fakeAutostart{}}).
		ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/autostart", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("状态码 = %d, 期望 405", rec.Code)
	}
}

// TestAutostartErrors 读写失败都回 500，且不假装成功。
func TestAutostartErrors(t *testing.T) {
	read := &fakeAutostart{readErr: errors.New("no registry")}
	rec := httptest.NewRecorder()
	handleAPIAutostart(Options{Autostart: read}).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/autostart", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("读失败状态码 = %d, 期望 500", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "no registry") {
		t.Fatalf("读失败响应未带原因: %s", rec.Body.String())
	}

	write := &fakeAutostart{setErr: errors.New("access denied")}
	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/autostart", strings.NewReader(`{"enabled":true}`))
	handleAPIAutostart(Options{Autostart: write}).ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("写失败状态码 = %d, 期望 500", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "access denied") {
		t.Fatalf("写失败响应未带原因: %s", rec.Body.String())
	}
}

// TestStateCapsAutostart 能力位跟随注入情况变化。
func TestStateCapsAutostart(t *testing.T) {
	has := capsOf(t, Options{Autostart: &fakeAutostart{}})
	if !has["autostart"] {
		t.Fatal("注入控制时 caps.autostart 应为 true")
	}
	none := capsOf(t, Options{})
	if none["autostart"] {
		t.Fatal("未注入控制时 caps.autostart 应为 false")
	}
}

// capsOf 取 /api/state 的 caps 位。
func capsOf(t *testing.T, opts Options) map[string]bool {
	t.Helper()
	api := &filesAPI{volumes: func() []string { return nil }}
	rec := httptest.NewRecorder()
	handleAPIState(api, opts).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/state", nil))
	var out struct {
		Caps map[string]bool `json:"caps"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("state 不是合法 JSON: %v (%s)", err, rec.Body.String())
	}
	return out.Caps
}
