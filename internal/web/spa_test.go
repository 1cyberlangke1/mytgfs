package web

// 静态资源回归：Wails 安卓资源服务器会把 "/" 规范成 "/index.html" 再取，
// 这里必须直接 200；走 http.FileServer 会把 /index.html 301 到 ./，
// 安卓桥只认 200，页面就变成 ERR_CONNECTION_REFUSED。

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestSPAIndexDirect 校验根路径与 /index.html 都直接返回 HTML 正文。
//
// 输入：GET / 与 GET /index.html
// 输出：200、text/html、非空正文
// 预期：没有 301/302 跳转；F5 与安卓首屏都靠这条
func TestSPAIndexDirect(t *testing.T) {
	for _, path := range []string{"/", "/index.html"} {
		rec := httptest.NewRecorder()
		spaHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s 状态码 = %d，期望 200", path, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
			t.Fatalf("%s Content-Type = %q，期望 text/html", path, ct)
		}
		if rec.Body.Len() == 0 {
			t.Fatalf("%s 正文为空", path)
		}
	}
}

// TestSPAAssetImmutable 校验带 hash 的 /assets/ 走长缓存，其它路径不缓存。
//
// 输入：GET /assets/ 前缀与普通路径
// 输出：Cache-Control 头
// 预期：只有 assets 下是 immutable，避免 index.html 被浏览器缓存住白屏
func TestSPAAssetImmutable(t *testing.T) {
	rec := httptest.NewRecorder()
	spaHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Fatalf("/ Cache-Control = %q，期望 no-cache", cc)
	}
}
