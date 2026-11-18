package web

// 操作队列接口测试：清空已结束记录必须落到服务端。

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"mytgfs/internal/queue"
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

// queueCancelRequest 造一个已填好路径参数的取消请求。
//
// 输入：method 请求方法；vol 卷名；id 任务号文本
// 输出：带 PathValue 的请求
// 预期：SetPathValue 与 ServeMux 的 {vol}/{id} 匹配结果一致
func queueCancelRequest(method, vol, id string) *http.Request {
	r := httptest.NewRequest(method, "/api/queue/"+vol+"/"+id, nil)
	r.SetPathValue("vol", vol)
	r.SetPathValue("id", id)
	return r
}

// TestQueueCancelUnavailable 入口没接线时返回 501。
func TestQueueCancelUnavailable(t *testing.T) {
	h := handleAPIQueueCancel(Options{})
	rec := httptest.NewRecorder()
	h(rec, queueCancelRequest(http.MethodDelete, "v1", "7"))
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("code = %d，期望 501", rec.Code)
	}
}

// TestQueueCancelOK 取消成功返回 200 与 cancelled 标记，并把卷名和任务号传给上层。
func TestQueueCancelOK(t *testing.T) {
	var gotVol string
	var gotID int64
	h := handleAPIQueueCancel(Options{CancelQueue: func(_ context.Context, vol string, id int64) error {
		gotVol, gotID = vol, id
		return nil
	}})
	rec := httptest.NewRecorder()
	h(rec, queueCancelRequest(http.MethodDelete, "sezhijian", "42"))
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d", rec.Code)
	}
	if gotVol != "sezhijian" || gotID != 42 {
		t.Fatalf("上层收到 vol=%q id=%d", gotVol, gotID)
	}
}

// TestQueueCancelBadID 任务号不是正整数时 400。
func TestQueueCancelBadID(t *testing.T) {
	called := false
	h := handleAPIQueueCancel(Options{CancelQueue: func(context.Context, string, int64) error {
		called = true
		return nil
	}})
	rec := httptest.NewRecorder()
	h(rec, queueCancelRequest(http.MethodDelete, "v1", "abc"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d，期望 400", rec.Code)
	}
	if called {
		t.Fatal("非法 id 不应调到上层")
	}
}

// TestQueueCancelNotFound 任务不存在映射 404。
func TestQueueCancelNotFound(t *testing.T) {
	h := handleAPIQueueCancel(Options{CancelQueue: func(context.Context, string, int64) error {
		return queue.ErrJobNotFound
	}})
	rec := httptest.NewRecorder()
	h(rec, queueCancelRequest(http.MethodDelete, "v1", "9"))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("code = %d，期望 404", rec.Code)
	}
}

// TestQueueCancelFinished 任务已结束映射 409。
func TestQueueCancelFinished(t *testing.T) {
	h := handleAPIQueueCancel(Options{CancelQueue: func(context.Context, string, int64) error {
		return queue.ErrJobFinished
	}})
	rec := httptest.NewRecorder()
	h(rec, queueCancelRequest(http.MethodDelete, "v1", "9"))
	if rec.Code != http.StatusConflict {
		t.Fatalf("code = %d，期望 409", rec.Code)
	}
}

// TestQueueCancelRejectsGet 只允许 DELETE。
func TestQueueCancelRejectsGet(t *testing.T) {
	h := handleAPIQueueCancel(Options{CancelQueue: func(context.Context, string, int64) error { return nil }})
	rec := httptest.NewRecorder()
	h(rec, queueCancelRequest(http.MethodGet, "v1", "9"))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("code = %d，期望 405", rec.Code)
	}
}
