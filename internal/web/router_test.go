package web

// 路由装配测试：写请求成功后一定推送，失败请求不推送。

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"mytgfs/internal/push"
)

// TestRouterPushesOnSuccessfulWrite 写请求 2xx 推送一次 status。
//
// 输入：POST /api/op，处理器返回 200
// 输出：记录器里有 1 次 status
// 预期：前端能触发的每个操作都有对应事件，不需要 handler 自己写
func TestRouterPushesOnSuccessfulWrite(t *testing.T) {
	rec := push.NewRecorder()
	rt := newRouter(rec)
	rt.routeFunc("/api/op", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	rw := httptest.NewRecorder()
	rt.ServeHTTP(rw, httptest.NewRequest(http.MethodPost, "/api/op", nil))

	if rw.Code != http.StatusOK {
		t.Fatalf("code=%d", rw.Code)
	}
	if got := rec.Count(push.TopicStatus); got != 1 {
		t.Fatalf("推送次数=%d，期望 1", got)
	}
}

// TestRouterSkipsPushOnFailure 失败请求不推送。
//
// 输入：POST 返回 400
// 输出：记录器为空
// 预期：参数错误不会让面板白刷一次
func TestRouterSkipsPushOnFailure(t *testing.T) {
	rec := push.NewRecorder()
	rt := newRouter(rec)
	rt.routeFunc("/api/op", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	})

	rt.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/op", nil))

	if got := rec.Count(push.TopicStatus); got != 0 {
		t.Fatalf("推送次数=%d，期望 0", got)
	}
}

// TestRouterSkipsPushOnRead 只读请求不推送。
//
// 输入：GET 返回 200
// 输出：记录器为空
// 预期：轮询/首屏读取不产生事件风暴
func TestRouterSkipsPushOnRead(t *testing.T) {
	rec := push.NewRecorder()
	rt := newRouter(rec)
	rt.routeFunc("/api/op", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	rt.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/op", nil))

	if got := rec.Count(push.TopicStatus); got != 0 {
		t.Fatalf("推送次数=%d，期望 0", got)
	}
}
