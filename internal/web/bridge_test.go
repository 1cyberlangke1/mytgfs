package web

// 安卓传输桥的单元测试：只验请求怎么进路由、响应怎么回前端。
// 桥只承载文本（JSON），文件字节走路径或原生通道，因此这里不出现任何编码。

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// TestBridgeDoForwardsRequest 确认方法、query 与文本正文都原样进路由。
//
// 输入：POST /api/x?a=1 + JSON 文本
// 输出：handler 收到的 method/query/body 与响应状态、正文
func TestBridgeDoForwardsRequest(t *testing.T) {
	var gotMethod, gotQuery, gotBody string
	b := NewBridge(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotQuery = r.URL.RawQuery
		raw, _ := io.ReadAll(r.Body)
		gotBody = string(raw)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))

	resp, err := b.Do(BridgeRequest{Method: "post", Path: "/api/x?a=1", Body: `{"a":1}`})
	if err != nil {
		t.Fatalf("Do 失败: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q，期望 POST", gotMethod)
	}
	if gotQuery != "a=1" {
		t.Errorf("query = %q，期望 a=1", gotQuery)
	}
	if gotBody != `{"a":1}` {
		t.Errorf("body = %q，期望原文本", gotBody)
	}
	if resp.Status != http.StatusAccepted {
		t.Errorf("status = %d，期望 202", resp.Status)
	}
	if resp.Type != "application/json" {
		t.Errorf("type = %q，期望 application/json", resp.Type)
	}
	if resp.Body != `{"ok":true}` {
		t.Errorf("body = %q，期望原文本", resp.Body)
	}
}

// TestBridgeDoEmptyMethodIsGet 确认空方法按 GET 处理，且空响应体回空串。
func TestBridgeDoEmptyMethodIsGet(t *testing.T) {
	var gotMethod string
	b := NewBridge(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		w.WriteHeader(http.StatusNoContent)
	}))
	resp, err := b.Do(BridgeRequest{Path: "/api/state"})
	if err != nil {
		t.Fatalf("Do 失败: %v", err)
	}
	if gotMethod != http.MethodGet {
		t.Errorf("method = %q，期望 GET", gotMethod)
	}
	if resp.Status != http.StatusNoContent {
		t.Errorf("status = %d，期望 204", resp.Status)
	}
	if resp.Body != "" {
		t.Errorf("空响应体应返回空串，得到 %q", resp.Body)
	}
}

// TestBridgeDoRejectsAssets 确认静态资源与 SSE 不走桥。
//
// 输入：非 /api/ 路径与 /api/events
// 输出：400，且底层 handler 一次都没被调用
func TestBridgeDoRejectsAssets(t *testing.T) {
	called := 0
	b := NewBridge(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called++ }))
	for _, path := range []string{"/", "/index.html", "/api/events"} {
		resp, err := b.Do(BridgeRequest{Path: path})
		if err != nil {
			t.Fatalf("%s: Do 失败: %v", path, err)
		}
		if resp.Status != http.StatusBadRequest {
			t.Errorf("%s: status = %d，期望 400", path, resp.Status)
		}
	}
	if called != 0 {
		t.Fatalf("底层 handler 不应被调用，实际 %d 次", called)
	}
}

// TestBridgeDoRejectsOversize 确认超过上限的正文被拒，而不是按声明长度分配内存。
func TestBridgeDoRejectsOversize(t *testing.T) {
	b := NewBridge(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("超限请求不应进 handler")
	}))
	resp, err := b.Do(BridgeRequest{Method: "POST", Path: "/api/x", Body: strings.Repeat("a", maxBridgeBody+1)})
	if err != nil {
		t.Fatalf("Do 失败: %v", err)
	}
	if resp.Status != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d，期望 413", resp.Status)
	}
}
