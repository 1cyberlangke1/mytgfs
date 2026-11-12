package web

// 面板 HTTP 层单测：建卷接口的状态码映射与页面标签注入。

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mytgfs/internal/config"
)

func TestHandleVolumesNotSupported(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/volumes", strings.NewReader(`{"name":"work"}`))
	handleVolumes(nil)(rec, req)
	if rec.Code != http.StatusNotImplemented {
		t.Errorf("code = %d", rec.Code)
	}
}

func TestHandleVolumesRejectsWrongMethod(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/volumes", nil)
	handleVolumes(nil)(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("code = %d", rec.Code)
	}
}

func TestHandleVolumesValidatesInput(t *testing.T) {
	called := false
	fn := func(context.Context, string, string, string) (any, error) {
		called = true
		return nil, nil
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/volumes", strings.NewReader(`not json`))
	handleVolumes(fn)(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad json code = %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/volumes", strings.NewReader(`{"name":"  "}`))
	handleVolumes(fn)(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("empty name code = %d", rec.Code)
	}
	if called {
		t.Error("create should not be called on invalid input")
	}
}

func TestHandleVolumesSuccess(t *testing.T) {
	var gotName, gotTitle, gotPoint string
	fn := func(_ context.Context, name, title, point string) (any, error) {
		gotName, gotTitle, gotPoint = name, title, point
		return map[string]any{"name": name, "muted": true}, nil
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/volumes",
		strings.NewReader(`{"name":"work","title":"工作盘","point":"Y:"}`))
	handleVolumes(fn)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d body = %s", rec.Code, rec.Body.String())
	}
	if gotName != "work" || gotTitle != "工作盘" || gotPoint != "Y:" {
		t.Errorf("args = %q/%q/%q", gotName, gotTitle, gotPoint)
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("json: %v", err)
	}
	if out["muted"] != true {
		t.Errorf("body = %v", out)
	}
}

func TestHandleVolumesConflict(t *testing.T) {
	fn := func(context.Context, string, string, string) (any, error) {
		return nil, config.ErrVolumeExists
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/volumes", strings.NewReader(`{"name":"work"}`))
	handleVolumes(fn)(rec, req)
	if rec.Code != http.StatusConflict {
		t.Errorf("code = %d", rec.Code)
	}
}

func TestPageInjectsLabels(t *testing.T) {
	rec := httptest.NewRecorder()
	handlePage(true)(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	body := rec.Body.String()
	if !strings.Contains(body, `"canCreate":"1"`) {
		t.Error("canCreate label missing")
	}
	if strings.Contains(body, "__LABELS__") {
		t.Error("labels placeholder not replaced")
	}

	rec = httptest.NewRecorder()
	handlePage(false)(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if strings.Contains(rec.Body.String(), `"canCreate":"1"`) {
		t.Error("canCreate should be absent when volume creation is disabled")
	}
}
