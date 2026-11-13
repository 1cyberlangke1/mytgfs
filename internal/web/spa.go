package web

// 前端静态资源：把 web/ 的构建产物内嵌进二进制。
//
// 构建方式：cd web && npx vite build，然后把 web/dist 覆盖到 internal/web/dist。
// 未命中的路径回落 index.html，前端自己按 hash 路由。

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:dist
var distFS embed.FS

// spaHandler 提供内嵌前端。
//
// 输入：任意 GET/HEAD 请求
// 输出：静态文件；未知路径返回 index.html
// 预期：index.html 与 sw.js 不缓存，带 hash 的 /assets/ 长缓存
func spaHandler() http.Handler {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic(err)
	}
	files := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "" {
			p = "index.html"
		}
		if strings.HasPrefix(p, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		if f, oerr := sub.Open(p); oerr == nil {
			_ = f.Close()
			files.ServeHTTP(w, r)
			return
		}
		// 前端路由：未知路径交给 index.html
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/"
		w.Header().Set("Cache-Control", "no-cache")
		files.ServeHTTP(w, r2)
	})
}
