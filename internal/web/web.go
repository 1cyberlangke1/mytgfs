package web

// 本地 Web 面板：查看连接/卷状态、触发增量同步、编辑配置。
//
// 只绑定 127.0.0.1；页面是嵌进二进制的静态前端，不依赖外网 CDN。
// 文案全部走 i18n，服务端渲染时注入语言包。

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/natefinch/atomic"
	"mytgfs/internal/config"
	"mytgfs/internal/daemon"
	"mytgfs/internal/i18n"
	"mytgfs/internal/ipc"
)

// StatusFunc 返回面板要展示的状态；nil 时回落到 daemon IPC。
type StatusFunc func(ctx context.Context) (any, error)

// SyncFunc 立即触发一次增量同步并返回结果。
type SyncFunc func(ctx context.Context) (any, error)

// CreateVolumeFunc 创建新卷并返回可序列化的结果。
//
// 输入：ctx；name 卷名；title 频道标题；point 挂载点
// 输出：结果对象；error
// 预期：重名返回 config.ErrVolumeExists，供 handler 映射成 409
type CreateVolumeFunc func(ctx context.Context, name, title, point string) (any, error)

// Options 是面板的可选能力，零值表示只提供配置编辑。
type Options struct {
	Status StatusFunc
	Sync   SyncFunc
	// CreateVolume 为 nil 时建卷接口返回 501，面板按钮置灰
	CreateVolume CreateVolumeFunc
}

// Serve 启动只读面板，状态回落到 daemon IPC。
func Serve(ctx context.Context, addr, configPath string) error {
	return ServeWith(ctx, addr, configPath, Options{})
}

// ServeWith 启动面板，阻塞到 ctx 取消。
//
// 输入：ctx；addr 监听地址；configPath 配置文件；opts 状态/同步钩子
// 输出：error
// 预期：ctx 取消时优雅关闭；opts.Status 为 nil 时尝试连 daemon
func ServeWith(ctx context.Context, addr, configPath string, opts Options) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", handlePage(opts.CreateVolume != nil))
	mux.HandleFunc("/api/status", handleStatus(opts.Status))
	mux.HandleFunc("/api/sync", handleSync(opts.Sync))
	mux.HandleFunc("/api/volumes", handleVolumes(opts.CreateVolume))
	mux.HandleFunc("/api/config", handleConfig(configPath))

	srv := &http.Server{Addr: addr, Handler: mux}
	go func() {
		<-ctx.Done()
		_ = srv.Shutdown(context.Background())
	}()
	err := srv.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

// handlePage 输出内嵌的静态页面，并把当前语言的文案注入进去。
//
// 输入：canCreate 是否支持建卷（决定是否显示建卷卡片）
// 输出：HTML
// 预期：语言包以 JSON 注入，前端不硬编码任何用户可见文案
func handlePage(canCreate bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		labels := map[string]string{
			"title":      i18n.T("web.title"),
			"subtitle":   i18n.T("web.subtitle"),
			"volumes":    i18n.T("web.volumes"),
			"actions":    i18n.T("web.actions"),
			"sync":       i18n.T("web.sync"),
			"syncing":    i18n.T("web.syncing"),
			"syncDone":   i18n.T("web.sync_done"),
			"config":     i18n.T("web.config"),
			"save":       i18n.T("web.save"),
			"saved":      i18n.T("web.saved"),
			"entries":    i18n.T("web.entries"),
			"used":       i18n.T("web.used"),
			"nodaemon":   i18n.T("web.no_daemon"),
			"loadError":  i18n.T("web.load_error"),
			"newVolume":  i18n.T("web.new_volume"),
			"fieldName":  i18n.T("web.field_name"),
			"fieldTitle": i18n.T("web.field_title"),
			"fieldPoint": i18n.T("web.field_point"),
			"create":     i18n.T("web.create"),
			"creating":   i18n.T("web.creating"),
			"created":    i18n.T("web.created"),
			"restart":    i18n.T("web.restart_hint"),
			"authLost":   i18n.T("web.auth_lost"),
			"nameNeed":   i18n.T("web.name_required"),
			"statVols":   i18n.T("web.stat_volumes"),
			"statFiles":  i18n.T("web.stat_files"),
			"statUsed":   i18n.T("web.stat_used"),
			"xfer":       i18n.T("web.xfer"),
			"footer":     i18n.T("web.footer"),
			"lang":       i18n.Lang(),
			"themeTip":   i18n.T("web.theme_tip"),
			"refreshTip": i18n.T("web.refresh_tip"),
		}
		if canCreate {
			labels["canCreate"] = "1"
		}
		blob, _ := json.Marshal(labels)
		// 用标准库 html/template 渲染，标签以 JS 字面量注入
		if err := pageTmpl.Execute(w, struct{ Labels template.JS }{Labels: template.JS(blob)}); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}

// pageTmpl 是内嵌的面板页面模板，__LABELS__ 换成 {{.Labels}}。
var pageTmpl = template.Must(template.New("page").Parse(pageHTML))

// handleStatus 优先用调用方注入的状态，否则回落到 daemon IPC。
func handleStatus(status StatusFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if status != nil {
			v, err := status(r.Context())
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, v)
			return
		}
		st, err := daemon.ReadState()
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{
				"state":   i18n.T("web.no_daemon"),
				"volumes": []any{},
			})
			return
		}
		data, err := ipc.Call(r.Context(), st.Addr, st.Token, "status", nil)
		if err != nil {
			// daemon 状态文件残留但进程已退出：面板照常显示，不报 502
			writeJSON(w, http.StatusOK, map[string]any{
				"state":   i18n.T("web.no_daemon"),
				"volumes": []any{},
			})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(data)
	}
}

// handleSync 立即增量同步；未注入同步能力时返回 501。
func handleSync(sync SyncFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if sync == nil {
			writeJSON(w, http.StatusNotImplemented, map[string]string{"error": i18n.T("web.sync_unavailable")})
			return
		}
		v, err := sync(r.Context())
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, v)
	}
}

// handleVolumes 创建新卷（POST /api/volumes）。
//
// 输入：JSON {name,title,point}
// 输出：200 结果；400 参数错误；409 重名；501 当前模式不支持；502 Telegram 失败
// 预期：建频道/拉 bot/静音全部在调用方实现，这里只做参数校验与状态码映射
func handleVolumes(create CreateVolumeFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if create == nil {
			writeJSON(w, http.StatusNotImplemented, map[string]string{"error": i18n.T("web.create_unavailable")})
			return
		}
		var req struct {
			Name  string `json:"name"`
			Title string `json:"title"`
			Point string `json:"point"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if strings.TrimSpace(req.Name) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": i18n.T("web.name_required")})
			return
		}
		v, err := create(r.Context(), req.Name, req.Title, req.Point)
		if err != nil {
			code := http.StatusBadGateway
			if errors.Is(err, config.ErrVolumeExists) {
				code = http.StatusConflict
			}
			writeJSON(w, code, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, v)
	}
}

// handleConfig 读写配置文件。
func handleConfig(path string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			b, err := os.ReadFile(path)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = w.Write(b)
		case http.MethodPut:
			b, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			if err := atomic.WriteFile(path, bytes.NewReader(b)); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]string{"ok": "saved"})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

const pageHTML = `<!doctype html>
<html lang="zh-CN" data-theme="dark">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<meta name="color-scheme" content="dark light">
<title>mytgfs</title>
<script>
// 首屏前定主题：手动选择 > 系统偏好 > 深色。避免亮暗闪烁。
(function(){
  try {
    var t = localStorage.getItem('mytgfs-theme');
    if (t !== 'light' && t !== 'dark') {
      t = window.matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark';
    }
    document.documentElement.dataset.theme = t;
  } catch (e) { document.documentElement.dataset.theme = 'dark'; }
})();
</script>
<style>
:root{
  --bg0:#0b1220; --bg1:#101b30;
  --glow1:#17406a; --glow2:#123a57;
  --card:rgba(255,255,255,.055);
  --line:rgba(255,255,255,.10);
  --line-strong:rgba(255,255,255,.20);
  --text:#e8eefb; --muted:#8ea0bf;
  --blue:#2aa7e0; --blue-deep:#1b86bb;
  --green:#39d98a; --amber:#f5b94c; --red:#ff6b7a;
  --chip:rgba(255,255,255,.05);
  --btn-ghost:rgba(255,255,255,.06);
  --input:rgba(0,0,0,.28);
  --stat:rgba(255,255,255,.04);
  --shadow:0 18px 50px rgba(0,0,0,.25);
  --banner-bg:rgba(255,107,122,.12);
  --banner-line:rgba(255,107,122,.42);
  --banner-text:#ffd7db;
}
:root[data-theme="light"]{
  --bg0:#f7f9fc; --bg1:#eef2f9;
  --glow1:#cfe4f7; --glow2:#dde9fb;
  --card:#ffffff;
  --line:rgba(16,32,52,.10);
  --line-strong:rgba(16,32,52,.20);
  --text:#16202e; --muted:#5b6b80;
  --blue:#1b86bb; --blue-deep:#146a95;
  --green:#0f9d5b; --amber:#b7791f; --red:#d93a4b;
  --chip:rgba(16,32,52,.045);
  --btn-ghost:rgba(16,32,52,.05);
  --input:#f4f7fc;
  --stat:#f7f9fd;
  --shadow:0 14px 36px rgba(23,43,77,.10);
  --banner-bg:rgba(217,58,75,.08);
  --banner-line:rgba(217,58,75,.32);
  --banner-text:#a61f2e;
}
*{box-sizing:border-box}
html,body{height:100%}
body{
  margin:0; color:var(--text); font-family:system-ui,-apple-system,"Segoe UI",Roboto,"Noto Sans SC",sans-serif;
  background:
    radial-gradient(1200px 700px at 8% -10%, var(--glow1) 0%, transparent 55%),
    radial-gradient(900px 600px at 110% 10%, var(--glow2) 0%, transparent 50%),
    linear-gradient(160deg,var(--bg0),var(--bg1));
  min-height:100vh; padding:28px 18px 48px;
  transition:background-color .2s ease, color .2s ease;
}
.wrap{max-width:1000px;margin:0 auto}
header{display:flex;align-items:center;gap:14px;margin-bottom:22px}
header .logo{width:54px;height:54px;flex:none;filter:drop-shadow(0 8px 18px rgba(42,167,224,.35))}
header h1{margin:0;font-size:21px;letter-spacing:.2px}
header .sub{margin:2px 0 0;color:var(--muted);font-size:13px}
.pill{padding:7px 14px;border-radius:999px;font-size:13px;font-weight:600;
  border:1px solid var(--line); background:var(--chip); white-space:nowrap}
.pill.ok{color:var(--green);border-color:rgba(57,217,138,.4);background:rgba(57,217,138,.10)}
.pill.warn{color:var(--amber);border-color:rgba(245,185,76,.4);background:rgba(245,185,76,.10)}
.pill.bad{color:var(--red);border-color:rgba(255,107,122,.4);background:rgba(255,107,122,.10)}
.pill.ok::before,.pill.warn::before,.pill.bad::before{
  content:"";display:inline-block;width:7px;height:7px;border-radius:50%;
  background:currentColor;margin-right:8px;vertical-align:1px;
  box-shadow:0 0 10px currentColor;animation:breathe 2.4s ease-in-out infinite;
}
@keyframes breathe{50%{opacity:.35}}
.head-actions{margin-left:auto;display:flex;align-items:center;gap:10px}
main{display:grid;gap:16px;grid-template-columns:1fr;align-items:start}
@media(min-width:860px){main{grid-template-columns:1.15fr .85fr}}
/* 窄屏（手机 / 竖屏平板）：单列、按钮铺满、避免横向溢出 */
@media(max-width:640px){
  body{padding:18px 12px 36px}
  header{flex-wrap:wrap;gap:10px;margin-bottom:16px}
  header .logo{width:44px;height:44px}
  header h1{font-size:18px}
  header .sub{font-size:12px}
  .head-actions{margin-left:0;width:100%;justify-content:flex-end}
  .card{padding:14px;border-radius:14px}
  .vol{flex-wrap:wrap;align-items:flex-start}
  .vol .meta{margin-left:0;width:100%;text-align:left}
  .card button:not(.iconbtn){width:100%;justify-content:center}
  .form{grid-template-columns:1fr}
  textarea{min-height:140px}
  .row{gap:8px}
}
/* 超窄屏：状态条与标题各占一行 */
@media(max-width:400px){
  .head-actions{justify-content:space-between}
  .pill{padding:6px 10px;font-size:12px}
  .stats{grid-template-columns:1fr}
}
.card{background:var(--card);border:1px solid var(--line);border-radius:16px;padding:18px 18px 16px;
  box-shadow:var(--shadow);transition:border-color .18s ease, transform .18s ease, background-color .2s ease}
.card:hover{border-color:var(--line-strong);transform:translateY(-1px)}
.card h2{margin:0 0 14px;font-size:13px;font-weight:600;color:var(--muted);letter-spacing:.4px;text-transform:uppercase;
  display:flex;align-items:center;gap:8px}
.vol{display:flex;align-items:center;gap:12px;padding:12px 0;border-top:1px solid var(--line)}
.vol:first-of-type{border-top:0;padding-top:0}
.vol .dot{width:9px;height:9px;border-radius:50%;background:var(--green);box-shadow:0 0 12px var(--green);flex:none}
.vol .dot.busy{background:var(--blue);box-shadow:0 0 12px var(--blue);animation:breathe 1.2s ease-in-out infinite}
.vol .name{font-weight:600;font-size:15px}
.vol .meta{margin-left:auto;color:var(--muted);font-size:13px;text-align:right;line-height:1.5;font-variant-numeric:tabular-nums}
.empty{color:var(--muted);font-size:14px;padding:6px 0}
.stats{display:grid;grid-template-columns:repeat(auto-fit,minmax(110px,1fr));gap:10px;margin-bottom:16px}
.stat{background:var(--stat);border:1px solid var(--line);border-radius:12px;padding:10px 12px}
.stat .k{display:block;color:var(--muted);font-size:11px;letter-spacing:.4px;margin-bottom:4px}
.stat strong{font-size:17px;font-weight:650;font-variant-numeric:tabular-nums}
button{
  appearance:none;border:1px solid rgba(42,167,224,.45);background:linear-gradient(180deg,var(--blue),var(--blue-deep));
  color:#fff;font:inherit;font-weight:600;padding:10px 16px;border-radius:11px;cursor:pointer;
  transition:transform .06s ease, filter .15s ease, background-color .2s ease;
  display:inline-flex;align-items:center;gap:8px;
}
button:hover{filter:brightness(1.08)}
button:active{transform:translateY(1px)}
button:focus-visible{outline:2px solid var(--blue);outline-offset:2px}
button.ghost{background:var(--btn-ghost);border-color:var(--line);color:var(--text)}
button.iconbtn{padding:10px;border-radius:12px}
button.iconbtn svg{width:18px;height:18px}
.ic{width:16px;height:16px;stroke:currentColor;fill:none;stroke-width:2;
  stroke-linecap:round;stroke-linejoin:round;flex:none}
.theme-sun{display:none}
:root[data-theme="light"] .theme-sun{display:block}
:root[data-theme="light"] .theme-moon{display:none}
.row{display:flex;align-items:center;gap:12px;flex-wrap:wrap}
.msg{color:var(--muted);font-size:13px;min-height:18px}
input,textarea{
  width:100%;border-radius:12px;border:1px solid var(--line);
  background:var(--input);color:var(--text);outline:none;
  transition:border-color .15s ease, background-color .2s ease;
}
input{padding:10px 12px;font:inherit}
textarea{
  min-height:200px;resize:vertical;padding:14px;
  font:13px/1.6 ui-monospace,SFMono-Regular,Consolas,monospace;
}
input:focus,textarea:focus{border-color:rgba(42,167,224,.55)}
input::placeholder{color:var(--muted);opacity:.8}
textarea::-webkit-scrollbar{width:10px}
textarea::-webkit-scrollbar-thumb{background:var(--line-strong);border-radius:8px}
textarea::-webkit-scrollbar-thumb:hover{background:var(--muted)}
textarea::-webkit-scrollbar-track{background:transparent}
.form{display:grid;grid-template-columns:repeat(auto-fit,minmax(170px,1fr));gap:12px}
.banner{grid-column:1/-1;border-radius:14px;padding:12px 16px;font-size:13px;line-height:1.6;display:none;
  color:var(--banner-text);background:var(--banner-bg);border:1px solid var(--banner-line)}
footer{margin-top:22px;color:var(--muted);font-size:12px;text-align:center}
</style>
</head>
<body>
<div class="wrap">
  <header>
    <svg class="logo" viewBox="0 0 256 256" aria-hidden="true">
      <circle cx="128" cy="118" r="115" fill="#0f5a80"/>
      <circle cx="128" cy="118" r="108" fill="#229ED9"/>
      <path d="M44 134 L218 56 L143 202 Z" fill="#fff"/>
      <path d="M120 157 L218 56 L143 202 Z" fill="#cdebf9"/>
      <circle cx="195" cy="195" r="63" fill="rgba(0,0,0,.20)"/>
      <circle cx="193" cy="192" r="61" fill="#fff"/>
      <rect x="153" y="170" width="78" height="44" rx="10" fill="#4a5866"/>
      <rect x="153" y="170" width="78" height="15" rx="7" fill="#707f8d"/>
      <rect x="163" y="190" width="58" height="5" rx="2.5" fill="#212d38"/>
      <circle cx="221" y="203" r="5" fill="#3ddc84"/>
    </svg>
    <div>
      <h1 id="l-title">mytgfs</h1>
      <p class="sub" id="l-subtitle"></p>
    </div>
    <div class="head-actions">
      <button id="theme" class="ghost iconbtn" type="button">
        <svg class="ic theme-sun" viewBox="0 0 24 24" aria-hidden="true">
          <circle cx="12" cy="12" r="4"/>
          <path d="M12 2v2M12 20v2M4.93 4.93l1.41 1.41M17.66 17.66l1.41 1.41M2 12h2M20 12h2M6.34 17.66l-1.41 1.41M19.07 4.93l-1.41 1.41"/>
        </svg>
        <svg class="ic theme-moon" viewBox="0 0 24 24" aria-hidden="true">
          <path d="M12 3a6 6 0 0 0 9 9 9 9 0 1 1-9-9Z"/>
        </svg>
      </button>
      <span id="pill" class="pill">…</span>
    </div>
  </header>

  <main>
    <div id="authbar" class="banner"></div>

    <section class="card">
      <h2>
        <svg class="ic" viewBox="0 0 24 24" aria-hidden="true">
          <line x1="22" x2="2" y1="12" y2="12"/>
          <path d="M5.45 5.11 2 12v6a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2v-6l-3.45-6.89A2 2 0 0 0 16.76 4H7.24a2 2 0 0 0-1.79 1.11Z"/>
          <line x1="6" x2="6.01" y1="16" y2="16"/>
          <line x1="10" x2="10.01" y1="16" y2="16"/>
        </svg>
        <span id="l-volumes"></span>
      </h2>
      <div id="volumes"><div class="empty">…</div></div>
    </section>

    <section class="card">
      <h2>
        <svg class="ic" viewBox="0 0 24 24" aria-hidden="true">
          <path d="M22 12h-4l-3 9L9 3l-3 9H2"/>
        </svg>
        <span id="l-actions"></span>
      </h2>
      <div class="stats">
        <div class="stat"><span class="k" id="l-stat-vols"></span><strong id="stat-vols">0</strong></div>
        <div class="stat"><span class="k" id="l-stat-files"></span><strong id="stat-files">0</strong></div>
        <div class="stat"><span class="k" id="l-stat-used"></span><strong id="stat-used">—</strong></div>
      </div>
      <div class="row">
        <button id="sync">
          <svg class="ic" viewBox="0 0 24 24" aria-hidden="true">
            <path d="M21 12a9 9 0 1 1-9-9c2.52 0 4.85 1.03 6.53 2.7L21 8"/>
            <path d="M21 3v5h-5"/>
          </svg>
          <span id="l-sync"></span>
        </button>
        <button id="reload" class="ghost iconbtn" type="button">
          <svg class="ic" viewBox="0 0 24 24" aria-hidden="true">
            <path d="M3 12a9 9 0 1 0 9-9 9.75 9.75 0 0 0-6.74 2.74L3 8"/>
            <path d="M3 3v5h5"/>
          </svg>
        </button>
      </div>
      <p class="msg" id="syncmsg"></p>
    </section>

    <section class="card" style="grid-column:1/-1">
      <h2>
        <svg class="ic" viewBox="0 0 24 24" aria-hidden="true">
          <path d="M20 20a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.9a2 2 0 0 1-1.69-.9L9.6 3.9A2 2 0 0 0 7.93 3H4a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2Z"/>
          <path d="M12 10v6M9 13h6"/>
        </svg>
        <span id="l-newvol"></span>
      </h2>
      <div class="form">
        <input id="nv-name" autocomplete="off">
        <input id="nv-title" autocomplete="off">
        <input id="nv-point" autocomplete="off">
      </div>
      <div class="row" style="margin-top:12px">
        <button id="nv-create">
          <svg class="ic" viewBox="0 0 24 24" aria-hidden="true">
            <path d="M12 5v14M5 12h14"/>
          </svg>
          <span id="l-create"></span>
        </button>
        <span class="msg" id="nv-msg"></span>
      </div>
    </section>

    <section class="card" style="grid-column:1/-1">
      <h2>
        <svg class="ic" viewBox="0 0 24 24" aria-hidden="true">
          <path d="M12 20h9"/>
          <path d="M16.5 3.5a2.12 2.12 0 0 1 3 3L7 19l-4 1 1-4Z"/>
        </svg>
        <span id="l-config"></span>
      </h2>
      <textarea id="cfg" spellcheck="false"></textarea>
      <div class="row" style="margin-top:12px">
        <button id="save">
          <svg class="ic" viewBox="0 0 24 24" aria-hidden="true">
            <path d="M19 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h11l5 5v11a2 2 0 0 1-2 2Z"/>
            <path d="M17 21v-8H7v8M7 3v5h8"/>
          </svg>
          <span id="l-save"></span>
        </button>
        <span class="msg" id="savemsg"></span>
      </div>
    </section>
  </main>
  <footer id="l-footer"></footer>
</div>
<script>
const L = {{.Labels}};
const $ = id => document.getElementById(id);
document.documentElement.lang = L.lang;
$('l-title').textContent = L.title;
$('l-subtitle').textContent = L.subtitle;
$('l-volumes').textContent = L.volumes;
$('l-actions').textContent = L.actions;
$('l-config').textContent = L.config;
$('l-newvol').textContent = L.newVolume;
$('l-sync').textContent = L.sync;
$('l-save').textContent = L.save;
$('l-create').textContent = L.create;
$('l-stat-vols').textContent = L.statVols;
$('l-stat-files').textContent = L.statFiles;
$('l-stat-used').textContent = L.statUsed;
$('l-footer').textContent = L.footer;
$('nv-name').placeholder = L.fieldName;
$('nv-title').placeholder = L.fieldTitle;
$('nv-point').placeholder = L.fieldPoint;
$('authbar').textContent = L.authLost;
$('reload').title = L.refreshTip;
$('reload').setAttribute('aria-label', L.refreshTip);
if (L.canCreate !== '1') { $('l-newvol').closest('section').style.display = 'none'; }

// 深浅色：跟随系统，允许手动覆盖并记住选择
const root = document.documentElement;
const themeBtn = $('theme');
function paintTheme(){
  themeBtn.title = L.themeTip;
  themeBtn.setAttribute('aria-label', L.themeTip);
  themeBtn.setAttribute('aria-pressed', root.dataset.theme === 'light' ? 'true' : 'false');
}
themeBtn.onclick = () => {
  root.dataset.theme = root.dataset.theme === 'light' ? 'dark' : 'light';
  try { localStorage.setItem('mytgfs-theme', root.dataset.theme); } catch (e) {}
  paintTheme();
};
paintTheme();
if (window.matchMedia) {
  const mq = window.matchMedia('(prefers-color-scheme: light)');
  const onChange = (e) => {
    try { if (localStorage.getItem('mytgfs-theme')) return; } catch (err) {}
    root.dataset.theme = e.matches ? 'light' : 'dark';
    paintTheme();
  };
  if (mq.addEventListener) mq.addEventListener('change', onChange);
  else if (mq.addListener) mq.addListener(onChange);
}

let lastState = '', lastKind = '';
function setPill(state, kind){
  const p = $('pill');
  p.textContent = state || '—';
  p.className = 'pill ' + (kind || '');
}
function fmtBytes(n){
  if (n === null || n === undefined) return '—';
  const u = ['B','KB','MB','GB','TB']; let i = 0, v = Number(n);
  while (v >= 1024 && i < u.length-1) { v /= 1024; i++; }
  return (i === 0 ? v : v.toFixed(1)) + ' ' + u[i];
}
function renderVolumes(data){
  const box = $('volumes');
  const vols = Array.isArray(data.volumes) ? data.volumes : [];
  let files = 0, used = 0;
  for (const v of vols) { files += Number(v.entries) || 0; used += Number(v.used) || 0; }
  $('stat-vols').textContent = String(vols.length);
  $('stat-files').textContent = String(files);
  $('stat-used').textContent = fmtBytes(used);
  if (!vols.length) {
    box.innerHTML = '<div class="empty">' + (data.entries !== undefined
      ? L.entries + ': ' + data.entries : L.nodaemon) + '</div>';
    return;
  }
  box.innerHTML = '';
  for (const v of vols) {
    const el = document.createElement('div');
    el.className = 'vol';
    const dot = document.createElement('span'); dot.className = 'dot';
    const name = document.createElement('span'); name.className = 'name'; name.textContent = v.name;
    const meta = document.createElement('span'); meta.className = 'meta';
    meta.textContent = L.entries + ': ' + (v.entries ?? 0) + ' · ' + L.used + ': ' + fmtBytes(v.used);
    el.append(dot, name, meta);
    box.append(el);
  }
  // 正在下载的文件：显示百分比与字节数
  const xf = Array.isArray(data.transfers) ? data.transfers : [];
  for (const t of xf) {
    const pct = t.total > 0 ? Math.min(100, Math.round(Number(t.done) * 100 / Number(t.total))) : 0;
    const el = document.createElement('div');
    el.className = 'vol';
    const dot = document.createElement('span'); dot.className = 'dot busy';
    const name = document.createElement('span'); name.className = 'name'; name.textContent = t.path;
    const meta = document.createElement('span'); meta.className = 'meta';
    meta.textContent = L.xfer + ' ' + pct + '% · ' + fmtBytes(t.done) + ' / ' + fmtBytes(t.total);
    el.append(dot, name, meta);
    box.append(el);
  }
}
async function refresh(){
  try {
    const r = await fetch('/api/status');
    const data = await r.json();
    if (data.error) { setPill(data.error, 'bad'); return; }
    const lost = data.auth === 'lost';
    $('authbar').style.display = lost ? 'block' : 'none';
    $('sync').disabled = lost;
    $('nv-create').disabled = lost;
    const s = String(data.state || '');
    const kind = lost ? 'bad'
      : /connect|已连接|connecting|ready/i.test(s) && !/reconnect|断开|disconnect/i.test(s) ? 'ok'
      : /reconnect|重连|disconnect|断开/i.test(s) ? 'warn' : '';
    if (s !== lastState || kind !== lastKind) { lastState = s; lastKind = kind; setPill(s, kind); }
    renderVolumes(data);
  } catch (e) {
    setPill(L.loadError, 'bad');
  }
}
$('sync').onclick = async () => {
  const b = $('sync'); b.disabled = true; $('syncmsg').textContent = L.syncing;
  try {
    const r = await fetch('/api/sync', {method:'POST'});
    const data = await r.json();
    $('syncmsg').textContent = data.error ? data.error : L.syncDone;
    await refresh();
  } catch (e) { $('syncmsg').textContent = String(e); }
  finally { b.disabled = false; }
};
$('reload').onclick = refresh;
fetch('/api/config').then(r=>r.text()).then(t=>{ $('cfg').value = t; });
$('save').onclick = async () => {
  $('savemsg').textContent = '';
  const r = await fetch('/api/config', {method:'PUT', body: $('cfg').value});
  const data = await r.json();
  $('savemsg').textContent = data.error || L.saved;
};
$('nv-create').onclick = async () => {
  const name = $('nv-name').value.trim();
  if (!name) { $('nv-msg').textContent = L.nameNeed; return; }
  const b = $('nv-create');
  b.disabled = true; $('nv-msg').textContent = L.creating;
  try {
    const r = await fetch('/api/volumes', {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({
        name,
        title: $('nv-title').value.trim(),
        point: $('nv-point').value.trim(),
      }),
    });
    const data = await r.json();
    if (data.error) {
      $('nv-msg').textContent = data.error;
    } else {
      $('nv-msg').textContent = L.created + ' · ' + L.restart;
      $('nv-name').value = ''; $('nv-title').value = ''; $('nv-point').value = '';
      fetch('/api/config').then(x=>x.text()).then(t=>{ $('cfg').value = t; });
      await refresh();
    }
  } catch (e) {
    $('nv-msg').textContent = String(e);
  } finally {
    b.disabled = false;
  }
};
refresh();
setInterval(refresh, 3000);
</script>
</body>
</html>`
