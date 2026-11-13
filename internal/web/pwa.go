package web

// PWA 支持：manifest、service worker 与图标。
//
// 只做三件事：
//   - 让浏览器能把面板装成独立窗口
//   - 预缓存外壳，断网也能打开面板
//   - 给「离线保存」提供图标与元数据
//
// 所有资源都嵌在二进制里，不依赖外网。

import (
	"cmp"
	_ "embed"
	"encoding/json"
	"net/http"
	"strings"

	"mytgfs/internal/config"
	"mytgfs/internal/i18n"
	"mytgfs/internal/version"
)

//go:embed static/sw.js
var swJS []byte

//go:embed static/icon-192.png
var icon192 []byte

//go:embed static/icon-512.png
var icon512 []byte

//go:embed static/apple-touch-icon.png
var appleTouchIcon []byte

// pwaData 是模板里用到的 PWA 设置。
type pwaData struct {
	Enabled bool
	Name    string
	// OfflineMax 是单个文件离线保存上限（字节），0 表示不限制
	OfflineMax int64
}

// newPWAData 从配置构造模板数据。
func newPWAData(p config.PWA) pwaData {
	return pwaData{Enabled: p.On(), Name: p.AppName(), OfflineMax: p.OfflineLimitBytes()}
}

// manifestIcon 是 manifest 里的一条图标声明。
type manifestIcon struct {
	Src     string `json:"src"`
	Sizes   string `json:"sizes"`
	Type    string `json:"type"`
	Purpose string `json:"purpose,omitempty"`
}

// webManifest 是 manifest.webmanifest 的内容。
type webManifest struct {
	Name            string         `json:"name"`
	ShortName       string         `json:"short_name"`
	Description     string         `json:"description"`
	StartURL        string         `json:"start_url"`
	Scope           string         `json:"scope"`
	Display         string         `json:"display"`
	Orientation     string         `json:"orientation"`
	BackgroundColor string         `json:"background_color"`
	ThemeColor      string         `json:"theme_color"`
	Lang            string         `json:"lang"`
	Categories      []string       `json:"categories"`
	Icons           []manifestIcon `json:"icons"`
}

// handleManifest 输出 PWA 清单（GET /manifest.webmanifest）。
//
// 输入：pwa 配置（应用名与启停）
// 输出：application/manifest+json
// 预期：
//   - 关掉 PWA 时返回 404，浏览器不会出现安装入口
//   - 名字与语言跟随配置和 i18n
func handleManifest(pwa config.PWA) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !pwa.On() {
			http.NotFound(w, r)
			return
		}
		m := webManifest{
			Name:            pwa.AppName(),
			ShortName:       pwa.AppName(),
			Description:     i18n.T("web.manifest_desc"),
			StartURL:        "/",
			Scope:           "/",
			Display:         "standalone",
			Orientation:     "any",
			BackgroundColor: "#0b1220",
			ThemeColor:      "#0b1220",
			Lang:            i18n.Lang(),
			Categories:      []string{"productivity", "utilities"},
			Icons: []manifestIcon{
				{Src: "/static/icon-192.png", Sizes: "192x192", Type: "image/png", Purpose: "any"},
				{Src: "/static/icon-512.png", Sizes: "512x512", Type: "image/png", Purpose: "any"},
				// 图形落在中心 80% 安全区，同一张图可直接当 maskable
				{Src: "/static/icon-512.png", Sizes: "512x512", Type: "image/png", Purpose: "maskable"},
			},
		}
		w.Header().Set("Content-Type", "application/manifest+json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_ = json.NewEncoder(w).Encode(m)
	}
}

// handleServiceWorker 输出 service worker（GET /sw.js）。
//
// 输入：无
// 输出：application/javascript
// 预期：
//   - 每次都回源校验（no-cache），发布新版本能立刻换缓存名
//   - 版本号注入到脚本里，作为缓存命名空间
func handleServiceWorker() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ver := cmp.Or(version.Version, "dev")
		ver = strings.NewReplacer(`"`, "", `\`, "", "\n", "", "\r", "").Replace(ver)
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		// 允许 SW 接管整站；本文件已位于根路径，这行是显式说明
		w.Header().Set("Service-Worker-Allowed", "/")
		_, _ = w.Write([]byte(strings.ReplaceAll(string(swJS), "__VERSION__", ver)))
	}
}
