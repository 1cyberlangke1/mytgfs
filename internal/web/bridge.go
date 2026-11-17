package web

// 安卓传输桥：把面板路由暴露成一个 Wails 服务方法。
//
// 输入：前端经 Wails 运行期桥送来的 {method, path, body(base64)}
// 输出：{status, type, body(base64)}
// 预期：与直接 fetch 走同一套 newMux 路由，行为一致；
// 安卓 WebView 的 shouldInterceptRequest 拿不到请求体、也会丢掉 query 串，
// 所以安卓前端不再直连 /api/*，统一从 Do 进来

import (
	"bytes"
	"encoding/base64"
	"net/http"
	"strings"
)

// BridgeRequest 是一次面板请求。
//
// Method 为空时按 GET 处理；Path 必须是 /api/ 开头且带完整 query。
type BridgeRequest struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	Body   string `json:"body"`
}

// BridgeResponse 是面板响应。
//
// Status 是 HTTP 状态码；Type 是响应的 Content-Type；Body 是原始字节的 base64。
type BridgeResponse struct {
	Status int    `json:"status"`
	Type   string `json:"type"`
	Body   string `json:"body"`
}

// Bridge 把 http.Handler 转成可被前端直接调用的服务。
type Bridge struct {
	h http.Handler
}

// NewBridge 包装面板路由。
//
// 输入：h 面板的完整路由（NewHandler 的返回值）
// 输出：*Bridge，注册进 application.Options.Services 后前端可 Call.ByName
func NewBridge(h http.Handler) *Bridge {
	return &Bridge{h: h}
}

// bridgeRecorder 收集一次响应的状态码、头与正文。
type bridgeRecorder struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (r *bridgeRecorder) Header() http.Header {
	if r.header == nil {
		r.header = http.Header{}
	}
	return r.header
}

func (r *bridgeRecorder) Write(p []byte) (int, error) { return r.body.Write(p) }

func (r *bridgeRecorder) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
}

// Do 执行一次面板请求。
//
// 输入：req.Method / req.Path / req.Body(base64)
// 输出：状态码、Content-Type 与 base64 正文
// 预期：非 /api/ 路径与 SSE 通道直接拒绝（前者走静态资源、后者阻塞不返回）
func (b *Bridge) Do(req BridgeRequest) (BridgeResponse, error) {
	method := strings.ToUpper(strings.TrimSpace(req.Method))
	if method == "" {
		method = http.MethodGet
	}
	if !strings.HasPrefix(req.Path, "/api/") {
		return BridgeResponse{Status: http.StatusBadRequest, Type: "application/json",
			Body: encodeBridgeBody([]byte(`{"error":{"message":"bridge only serves /api/"}}`))}, nil
	}
	if strings.HasPrefix(req.Path, "/api/events") {
		return BridgeResponse{Status: http.StatusBadRequest, Type: "application/json",
			Body: encodeBridgeBody([]byte(`{"error":{"message":"events use the desktop runtime"}}`))}, nil
	}

	var body []byte
	if req.Body != "" {
		raw, err := base64.StdEncoding.DecodeString(req.Body)
		if err != nil {
			return BridgeResponse{}, err
		}
		body = raw
	}
	hr, err := http.NewRequest(method, req.Path, bytes.NewReader(body))
	if err != nil {
		return BridgeResponse{}, err
	}
	// 上传分片按原始字节收，与桌面端同源
	if method != http.MethodGet {
		hr.Header.Set("Content-Type", "application/octet-stream")
	}
	rec := &bridgeRecorder{}
	b.h.ServeHTTP(rec, hr)
	status := rec.status
	if status == 0 {
		status = http.StatusOK
	}
	return BridgeResponse{
		Status: status,
		Type:   rec.header.Get("Content-Type"),
		Body:   encodeBridgeBody(rec.body.Bytes()),
	}, nil
}

// encodeBridgeBody 把字节编成 base64 字符串。
//
// 输入：原始字节
// 输出：base64（空输入返回空串，前端据此跳过解码）
func encodeBridgeBody(p []byte) string {
	if len(p) == 0 {
		return ""
	}
	return base64.StdEncoding.EncodeToString(p)
}
