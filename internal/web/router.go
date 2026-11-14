package web

// router 是面板唯一允许注册路由的地方。
//
// 约束的目的：写操作失败于「忘了推送」是最难发现的一类 bug。
// 所以这里不暴露 *http.ServeMux，只有 route 一个注册入口：
//
//   - route 注册的每个处理器，只要请求方法不是只读、响应 2xx，就自动推 status
//   - 想绕开就必须改这个文件（contract_test 会扫描并失败）
//
// 新增前端操作时不需要记得写推送，因为注册点已经替你写了。

import (
	"net/http"

	"mytgfs/internal/push"
)

// router 持有真实的 mux，并统一给写请求加推送。
type router struct {
	mux    *http.ServeMux
	events push.Events
}

// newRouter 创建面板路由。
//
// 输入：events 推送通道（*push.Hub 或测试记录器；nil 表示没有面板在监听）
// 输出：*router
// 预期：所有 /api/* 路由先于 "/" 注册，前端路由不会吞掉接口
func newRouter(events push.Events) *router {
	return &router{mux: http.NewServeMux(), events: events}
}

// route 注册一个处理器。
//
// 输入：pattern 路由；h 处理器
// 输出：无
// 预期：非只读方法返回 2xx 后自动推 status；GET/HEAD/OPTIONS 原样透传
// （SSE 需要真正的 http.Flusher）
func (rt *router) route(pattern string, h http.Handler) {
	rt.mux.Handle(pattern, rt.withWritePush(h))
}

// routeFunc 是 route 的函数版，给 http.HandlerFunc 用。
func (rt *router) routeFunc(pattern string, h http.HandlerFunc) {
	rt.route(pattern, h)
}

// ServeHTTP 让 router 直接当 http.Handler 用。
func (rt *router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rt.mux.ServeHTTP(w, r)
}

// withWritePush 给单个处理器包上「写操作成功即推送」。
//
// 输入：next 实际处理器
// 输出：包装后的处理器
// 预期：hub 为 nil 时原样返回；2xx 才推，失败请求不刷屏
func (rt *router) withWritePush(next http.Handler) http.Handler {
	if rt.events == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		if rec.status >= 200 && rec.status < 300 {
			rt.events.Publish(push.TopicStatus)
		}
	})
}

// statusRecorder 记录响应码，供写操作判断是否成功。
type statusRecorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

// WriteHeader 记住第一次写出的状态码。
//
// 输入：code HTTP 状态码
// 输出：无
// 预期：后续重复调用不再改写记录（与 net/http 行为一致）
func (s *statusRecorder) WriteHeader(code int) {
	if !s.wrote {
		s.status = code
		s.wrote = true
	}
	s.ResponseWriter.WriteHeader(code)
}
