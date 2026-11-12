package web

// 本地 Web 面板：查看连接/卷状态、触发增量同步、编辑配置。
//
// 只绑定 127.0.0.1；页面是嵌进二进制的静态前端，不依赖外网 CDN。
// 文案全部走 i18n，服务端渲染时注入语言包。

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"

	"mytgfs/internal/atomicfile"
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
		}
		if canCreate {
			labels["canCreate"] = "1"
		}
		blob, _ := json.Marshal(labels)
		_, _ = io.WriteString(w, strings.Replace(pageHTML, "__LABELS__", string(blob), 1))
	}
}

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
			if err := atomicfile.WriteFile(path, b, 0o600); err != nil {
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
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>mytgfs</title>
<style>
:root{
  --bg0:#0b1220; --bg1:#101b30; --card:rgba(255,255,255,.055);
  --line:rgba(255,255,255,.10); --text:#e8eefb; --muted:#8ea0bf;
  --blue:#2aa7e0; --green:#39d98a; --amber:#f5b94c; --red:#ff6b7a;
}
*{box-sizing:border-box}
html,body{height:100%}
body{
  margin:0; color:var(--text); font-family:system-ui,-apple-system,"Segoe UI",Roboto,"Noto Sans SC",sans-serif;
  background:
    radial-gradient(1200px 700px at 8% -10%, #17406a 0%, transparent 55%),
    radial-gradient(900px 600px at 110% 10%, #123a57 0%, transparent 50%),
    linear-gradient(160deg,var(--bg0),var(--bg1));
  min-height:100vh; padding:28px 18px 48px;
}
.wrap{max-width:980px;margin:0 auto}
header{display:flex;align-items:center;gap:14px;margin-bottom:22px}
header .logo{width:54px;height:54px;flex:none;filter:drop-shadow(0 8px 18px rgba(42,167,224,.35))}
header h1{margin:0;font-size:21px;letter-spacing:.2px}
header .sub{margin:2px 0 0;color:var(--muted);font-size:13px}
.pill{margin-left:auto;padding:7px 14px;border-radius:999px;font-size:13px;font-weight:600;
  border:1px solid var(--line); background:rgba(255,255,255,.05); white-space:nowrap}
.pill.ok{color:var(--green);border-color:rgba(57,217,138,.4);background:rgba(57,217,138,.10)}
.pill.warn{color:var(--amber);border-color:rgba(245,185,76,.4);background:rgba(245,185,76,.10)}
.pill.bad{color:var(--red);border-color:rgba(255,107,122,.4);background:rgba(255,107,122,.10)}
main{display:grid;gap:16px;grid-template-columns:1fr}
@media(min-width:860px){main{grid-template-columns:1.15fr .85fr}}
.card{background:var(--card);border:1px solid var(--line);border-radius:16px;padding:18px 18px 16px;
  backdrop-filter:blur(10px);box-shadow:0 18px 50px rgba(0,0,0,.25)}
.card h2{margin:0 0 14px;font-size:14px;font-weight:600;color:var(--muted);letter-spacing:.4px;text-transform:uppercase}
.vol{display:flex;align-items:center;gap:12px;padding:12px 0;border-top:1px solid var(--line)}
.vol:first-of-type{border-top:0;padding-top:0}
.vol .dot{width:9px;height:9px;border-radius:50%;background:var(--green);box-shadow:0 0 12px var(--green);flex:none}
.vol .name{font-weight:600}
.vol .meta{margin-left:auto;color:var(--muted);font-size:13px;text-align:right;line-height:1.5}
.empty{color:var(--muted);font-size:14px;padding:6px 0}
button{
  appearance:none;border:1px solid rgba(42,167,224,.45);background:linear-gradient(180deg,#2aa7e0,#1b86bb);
  color:#fff;font:inherit;font-weight:600;padding:10px 16px;border-radius:11px;cursor:pointer;
  transition:transform .06s ease, filter .15s ease;
}
button:hover{filter:brightness(1.08)}
button:active{transform:translateY(1px)}
button.ghost{background:rgba(255,255,255,.06);border-color:var(--line);color:var(--text)}
.row{display:flex;align-items:center;gap:12px;flex-wrap:wrap}
.msg{color:var(--muted);font-size:13px;min-height:18px}
textarea{
  width:100%;min-height:240px;resize:vertical;border-radius:12px;border:1px solid var(--line);
  background:rgba(0,0,0,.28);color:#dbe6fa;padding:14px;font:13px/1.6 ui-monospace,SFMono-Regular,Consolas,monospace;
  outline:none;
}
textarea:focus{border-color:rgba(42,167,224,.55)}
input{width:100%;padding:10px 12px;border-radius:10px;border:1px solid var(--line);
  background:rgba(0,0,0,.28);color:var(--text);font:inherit;outline:none}
input:focus{border-color:rgba(42,167,224,.55)}
.form{display:grid;grid-template-columns:repeat(auto-fit,minmax(170px,1fr));gap:12px}
.banner{grid-column:1/-1;border-radius:14px;padding:12px 16px;font-size:13px;line-height:1.6;display:none;
  color:#ffd7db;background:rgba(255,107,122,.12);border:1px solid rgba(255,107,122,.42)}
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
    <span id="pill" class="pill">…</span>
  </header>

  <main>
    <div id="authbar" class="banner"></div>

    <section class="card">
      <h2 id="l-volumes"></h2>
      <div id="volumes"><div class="empty">…</div></div>
    </section>

    <section class="card">
      <h2 id="l-actions"></h2>
      <div class="row">
        <button id="sync"></button>
        <button id="reload" class="ghost">↻</button>
      </div>
      <p class="msg" id="syncmsg"></p>
    </section>

    <section class="card" style="grid-column:1/-1">
      <h2 id="l-newvol"></h2>
      <div class="form">
        <input id="nv-name" autocomplete="off">
        <input id="nv-title" autocomplete="off">
        <input id="nv-point" autocomplete="off">
      </div>
      <div class="row" style="margin-top:12px">
        <button id="nv-create"></button>
        <span class="msg" id="nv-msg"></span>
      </div>
    </section>

    <section class="card" style="grid-column:1/-1">
      <h2 id="l-config"></h2>
      <textarea id="cfg" spellcheck="false"></textarea>
      <div class="row" style="margin-top:12px">
        <button id="save"></button>
        <span class="msg" id="savemsg"></span>
      </div>
    </section>
  </main>
  <footer>mytgfs · Telegram as a drive</footer>
</div>
<script>
const L = __LABELS__;
const $ = id => document.getElementById(id);
$('l-title').textContent = L.title;
$('l-subtitle').textContent = L.subtitle;
$('l-volumes').textContent = L.volumes;
$('l-actions').textContent = L.actions;
$('l-config').textContent = L.config;
$('sync').textContent = L.sync;
$('save').textContent = L.save;
$('l-newvol').textContent = L.newVolume;
$('nv-name').placeholder = L.fieldName;
$('nv-title').placeholder = L.fieldTitle;
$('nv-point').placeholder = L.fieldPoint;
$('nv-create').textContent = L.create;
$('authbar').textContent = L.authLost;
if (L.canCreate !== '1') { $('l-newvol').closest('section').style.display = 'none'; }

let lastState = '', lastKind = '';
function setPill(state, kind){
  const p = $('pill');
  p.textContent = state || '—';
  p.className = 'pill ' + (kind || '');
}
function renderVolumes(data){
  const box = $('volumes');
  const vols = Array.isArray(data.volumes) ? data.volumes : [];
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
}
function fmtBytes(n){
  if (n === null || n === undefined) return '—';
  const u = ['B','KB','MB','GB','TB']; let i = 0, v = Number(n);
  while (v >= 1024 && i < u.length-1) { v /= 1024; i++; }
  return (i === 0 ? v : v.toFixed(1)) + ' ' + u[i];
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
