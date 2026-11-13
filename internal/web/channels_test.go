package web

// 频道列表接口的单元测试：成功、能力未注入、上游报错、方法不对。

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"mytgfs/internal/channel"
)

// TestHandleAPIChannelsReturnsItems 正常返回 user 可管理的频道。
//
// 输入：Channels 返回两条摘要
// 输出：200 + channels 数组
// 预期：chat_id 原样透出，供前端直接填表
func TestHandleAPIChannelsReturnsItems(t *testing.T) {
	h := handleAPIChannels(Options{
		Channels: func(context.Context) ([]channel.Item, error) {
			return []channel.Item{
				{ID: 3536007396, ChatID: -1003536007396, Title: "demo", Creator: true},
				{ID: 3922700104, ChatID: -1003922700104, Title: "demo2", Admin: true},
			}, nil
		},
	})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/api/channels", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got struct {
		Channels []channel.Item `json:"channels"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Channels) != 2 {
		t.Fatalf("channels = %d, want 2", len(got.Channels))
	}
	if got.Channels[0].ChatID != -1003536007396 || !got.Channels[0].Creator {
		t.Errorf("unexpected first item: %+v", got.Channels[0])
	}
}

// TestHandleAPIChannelsNilItems 上游返回 nil 时输出空数组而不是 null。
func TestHandleAPIChannelsNilItems(t *testing.T) {
	h := handleAPIChannels(Options{
		Channels: func(context.Context) ([]channel.Item, error) { return nil, nil },
	})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/api/channels", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got struct {
		Channels []channel.Item `json:"channels"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Channels == nil {
		t.Fatal("channels 应为空数组而不是 null")
	}
}

// TestHandleAPIChannelsUnavailable 没注入能力时返回 501，前端据此隐藏下拉。
func TestHandleAPIChannelsUnavailable(t *testing.T) {
	rec := httptest.NewRecorder()
	handleAPIChannels(Options{})(rec, httptest.NewRequest(http.MethodGet, "/api/channels", nil))
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", rec.Code)
	}
}

// TestHandleAPIChannelsUpstreamError 上游错误（如缺 user 会话）原样带回 400。
func TestHandleAPIChannelsUpstreamError(t *testing.T) {
	h := handleAPIChannels(Options{
		Channels: func(context.Context) ([]channel.Item, error) {
			return nil, errors.New("no user session")
		},
	})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/api/channels", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if body := rec.Body.String(); body == "" {
		t.Fatal("错误原因应回传")
	}
}

// TestHandleAPIChannelsRejectsPost 只接受 GET。
func TestHandleAPIChannelsRejectsPost(t *testing.T) {
	rec := httptest.NewRecorder()
	handleAPIChannels(Options{})(rec, httptest.NewRequest(http.MethodPost, "/api/channels", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}
