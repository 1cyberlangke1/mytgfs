package web

// 账号接口测试：能力未注入、读取状态、保存凭证、缺少 api_id 时的错误透传。

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"mytgfs/internal/authsvc"
)

// newAuthService 造一个指向临时目录的登录服务。
//
// 输入：t；apiID 写进 config.toml
// 输出：*authsvc.Service
func newAuthService(t *testing.T, apiID int) *authsvc.Service {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	body := "[telegram]\napi_id = " + strconv.Itoa(apiID) + "\n"
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return authsvc.New(authsvc.Paths{
		ConfigPath:   cfgPath,
		APIHashFile:  filepath.Join(dir, "secrets", "api_hash"),
		BotTokenFile: filepath.Join(dir, "secrets", "bot_token"),
		BotSession:   filepath.Join(dir, "secrets", "bot.session"),
		UserSession:  filepath.Join(dir, "secrets", "user.session"),
		KeyPath:      filepath.Join(dir, "secrets", "session.key"),
	})
}

// TestAccountRoutesNeedAuth 没注入服务时所有账号接口都回 501，UI 据此隐藏卡片。
func TestAccountRoutesNeedAuth(t *testing.T) {
	handlers := map[string]http.HandlerFunc{
		"credentials": handleAPICredentials(Options{}),
		"login bot":   handleAPILoginBot(Options{}),
		"user start":  handleAPIUserStart(Options{}),
		"user finish": handleAPIUserFinish(Options{}),
		"identity":    handleAPIIdentity(Options{}),
	}
	for name, h := range handlers {
		method := http.MethodPost
		if name == "credentials" || name == "identity" {
			method = http.MethodGet
		}
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest(method, "/", nil))
		if rec.Code != http.StatusNotImplemented {
			t.Errorf("%s: status = %d, want 501", name, rec.Code)
		}
	}
}

// TestCredentialsGet 读取状态返回 api_id 与「已保存」标记。
func TestCredentialsGet(t *testing.T) {
	rec := httptest.NewRecorder()
	h := handleAPICredentials(Options{Auth: newAuthService(t, 778899)})
	h(rec, httptest.NewRequest(http.MethodGet, "/api/credentials", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var st authsvc.Status
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if st.APIID != 778899 {
		t.Fatalf("api_id = %d", st.APIID)
	}
	if st.APIHashSet || st.BotTokenSet {
		t.Fatalf("未填写时应为 false: %+v", st)
	}
}

// TestCredentialsSave 保存后回 restart_required，并写入密钥文件。
func TestCredentialsSave(t *testing.T) {
	svc := newAuthService(t, 1)
	rec := httptest.NewRecorder()
	body := strings.NewReader(`{"api_id":2468,"api_hash":"h","bot_token":"t"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/credentials", body)
	req.Header.Set("Content-Type", "application/json")
	handleAPICredentials(Options{Auth: svc})(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Status          authsvc.Status `json:"status"`
		RestartRequired bool           `json:"restart_required"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Status.APIID != 2468 || !out.Status.APIHashSet || !out.Status.BotTokenSet {
		t.Fatalf("status = %+v", out.Status)
	}
	if !out.RestartRequired {
		t.Fatal("改凭证后应提示重启")
	}
}

// TestLoginBotWithoutAPIID 缺 api_id 时回 400，把原因带给前端。
func TestLoginBotWithoutAPIID(t *testing.T) {
	svc := newAuthService(t, 0)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/login/bot", strings.NewReader(`{"token":"x"}`))
	handleAPILoginBot(Options{Auth: svc})(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestIdentityRejectsWrongMethod 只接受 GET。
func TestIdentityRejectsWrongMethod(t *testing.T) {
	rec := httptest.NewRecorder()
	handleAPIIdentity(Options{Auth: newAuthService(t, 1)})(rec, httptest.NewRequest(http.MethodPost, "/api/identity", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d", rec.Code)
	}
}
