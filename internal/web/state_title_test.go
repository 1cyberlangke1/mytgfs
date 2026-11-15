package web

// /api/state 的卷摘要：显示名来自频道标题，读不到时留给前端回落卷名。

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// stateVolumes 请求 /api/state 并取出卷摘要。
func stateVolumes(t *testing.T, opts Options) []volumeState {
	t.Helper()
	api := &filesAPI{volumes: func() []string { return []string{"demo", "demo2"} }}
	rec := httptest.NewRecorder()
	handleAPIState(api, opts).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/state", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d", rec.Code)
	}
	var out struct {
		Volumes []volumeState `json:"volumes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("state 不是合法 JSON: %v (%s)", err, rec.Body.String())
	}
	return out.Volumes
}

// TestStateCarriesVolumeTitle 频道标题随状态返回，前端显示成「标题(卷名)」。
func TestStateCarriesVolumeTitle(t *testing.T) {
	covers := &fakeCovers{
		volumes: map[string]bool{"demo": true, "demo2": true},
		title:   "我的盘",
	}
	vols := stateVolumes(t, Options{Cover: covers})
	if len(vols) != 2 {
		t.Fatalf("卷数 = %d, 期望 2", len(vols))
	}
	for _, v := range vols {
		if v.Name == "" {
			t.Fatalf("卷名不能为空: %+v", v)
		}
		if v.Title != "我的盘" {
			t.Fatalf("%s 的 title = %q, 期望 我的盘", v.Name, v.Title)
		}
	}
}

// TestStateTitleFailureFallsBack 标题读失败不算状态失败：title 为空，卷名照常返回。
func TestStateTitleFailureFallsBack(t *testing.T) {
	covers := &fakeCovers{
		volumes:  map[string]bool{"demo": true, "demo2": true},
		titleErr: errors.New("FLOOD_WAIT_30"),
	}
	vols := stateVolumes(t, Options{Cover: covers})
	if len(vols) != 2 {
		t.Fatalf("卷数 = %d, 期望 2（标题失败不能拖垮整页状态）", len(vols))
	}
	if vols[0].Title != "" || vols[0].Name == "" {
		t.Fatalf("标题失败时应为空串且保留卷名: %+v", vols[0])
	}
}
