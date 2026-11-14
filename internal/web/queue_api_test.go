package web

// 操作队列接口测试：清空已结束记录必须落到服务端。

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestQueueAPIUnavailable 入口没接线时返回 501。
func TestQueueAPIUnavailable(t *testing.T) {
	h := handleAPIQueue(Options{})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodDelete, "/api/queue", nil))
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("code = %d，期望 501", rec.Code)
	}
}

// TestQueueAPIClears 正常清空返回删除条数。
//
// 输入：ClearQueue 返回 3
// 输出：200 + {"removed":3}
// 预期：面板按钮拿到真实结果，不再只改本地数组
func TestQueueAPIClears(t *testing.T) {
	h := handleAPIQueue(Options{ClearQueue: func(context.Context) (int64, error) { return 3, nil }})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodDelete, "/api/queue", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d", rec.Code)
	}
	if got := rec.Body.String(); got != `{"removed":3}`+"\n" && got != `{"removed":3}` {
		t.Fatalf("body = %s", got)
	}
}

// TestQueueAPIRejectsGet 只允许 DELETE。
func TestQueueAPIRejectsGet(t *testing.T) {
	h := handleAPIQueue(Options{ClearQueue: func(context.Context) (int64, error) { return 0, nil }})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/api/queue", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("code = %d，期望 405", rec.Code)
	}
}
