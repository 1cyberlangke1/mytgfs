package trayapp

// 桌面壳的公共装配：进程内静态资源 + 安卓传输桥 + 首次启动的登录面板。

import (
	"context"
	"net/http"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"

	"mytgfs/internal/authsvc"
	"mytgfs/internal/brand"
	"mytgfs/internal/push"
	"mytgfs/internal/web"
)

// shellOptions 返回桌面与安卓共用的 Wails 选项。
//
// 输入：handler 面板路由（热替换包装后的出口）
// 输出：已填好 Assets 与 Services 的 Options
// 预期：不监听端口；静态资源与安卓桥走同一份 handler；调用方按平台补
// SingleInstance / Windows 等字段
func shellOptions(handler http.Handler) application.Options {
	return application.Options{
		Name:        "mytgfs",
		Description: "Telegram channel file volumes",
		Icon:        brand.Large,
		Assets:      application.AssetOptions{Handler: handler},
		Services:    []application.Service{application.NewService(web.NewBridge(handler))},
	}
}

// runSetupPanel 只开登录面板：没有 API 凭证时让用户先填凭证。
//
// 输入：cfgPath 配置文件路径
// 输出：Wails 主循环退出时返回的 error
// 预期：面板只提供账号卡与配置编辑；凭证保存后需要重启应用才进入完整模式
func runSetupPanel(cfgPath string) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	hub := push.NewHub(ctx)
	defer hub.Close()

	var hmu sync.RWMutex
	inner := http.Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<!doctype html><meta charset=utf-8><body style=\"font:14px system-ui;padding:24px\">mytgfs</body>"))
	}))
	setHandler := func(h http.Handler) { hmu.Lock(); inner = h; hmu.Unlock() }
	assets := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hmu.RLock()
		h := inner
		hmu.RUnlock()
		h.ServeHTTP(w, r)
	})

	wailsApp := application.New(shellOptions(assets))
	wailsApp.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:   "main",
		Title:  "mytgfs",
		Width:  1120,
		Height: 760,
		URL:    "/",
	})

	setHandler(web.NewHandler(cfgPath, web.Options{
		Auth: authsvc.New(authsvc.Paths{ConfigPath: cfgPath}),
		Push: hub,
	}))

	unsubscribe := hub.Subscribe(func(topic string) {
		wailsApp.Event.Emit(push.DesktopEvent(topic))
	})
	defer unsubscribe()

	return wailsApp.Run()
}
