package web

// 本地 Web 面板：查看连接/卷状态、触发增量同步、编辑配置。
//
// 只绑定 127.0.0.1；页面是嵌进二进制的静态前端，不依赖外网 CDN。
// 文案全部走 i18n，服务端渲染时注入语言包。

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
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

//go:embed static/htmx.min.js
var htmxJS []byte

// tmplFuncs 是模板里可用的辅助函数。
var tmplFuncs = template.FuncMap{
	"bytes": formatBytes,
}

// formatBytes 把字节数格式化成人类可读的字符串。
//
// 输入：n 字节数
// 输出：如 "0 B"、"12.3 MB"
// 预期：保留一位小数（字节级除外），与旧前端显示风格一致
func formatBytes(n int64) string {
	units := []string{"B", "KB", "MB", "GB", "TB"}
	f := float64(n)
	i := 0
	for f >= 1024 && i < len(units)-1 {
		f /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%d %s", n, units[i])
	}
	return fmt.Sprintf("%.1f %s", f, units[i])
}

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
	mux.HandleFunc("/", handlePage(opts.CreateVolume != nil, configPath))
	// htmx 随二进制分发：面板不依赖任何外网 CDN
	mux.HandleFunc("/static/htmx.min.js", handleAsset("application/javascript; charset=utf-8", htmxJS))
	// 片段接口：htmx 轮询/提交的目标，返回 HTML 而不是 JSON
	mux.HandleFunc("/frag/status", handleFragStatus(opts.Status))
	mux.HandleFunc("/frag/sync", handleFragSync(opts.Sync))
	mux.HandleFunc("/frag/config", handleFragConfig(configPath))
	mux.HandleFunc("/frag/volume", handleFragVolume(opts.CreateVolume, configPath))
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

// pageLabels 是页面全部文案，统一取自 i18n。
//
// 模板里不出现任何硬编码的用户可见字符串，换语言只改语言包。
type pageLabels struct {
	Title, Subtitle, Volumes, Actions                      string
	Sync, Syncing, SyncDone                                string
	Config, Save, Saved                                    string
	Entries, Used, NoDaemon, LoadError                     string
	NewVolume, FieldName, FieldTitle, FieldPoint           string
	Create, Creating, Created, Restart, AuthLost, NameNeed string
	StatVols, StatFiles, StatUsed, Xfer                    string
	Footer, Lang, ThemeTip, RefreshTip                     string
	// CanCreate 决定建卷卡片是否渲染
	CanCreate bool
}

// loadLabels 读取当前语言的页面文案。
//
// 输入：canCreate 是否支持建卷
// 输出：文案集合
// 预期：语言由 i18n.Lang() 决定，与托盘/CLI 保持一致
func loadLabels(canCreate bool) pageLabels {
	return pageLabels{
		Title:      i18n.T("web.title"),
		Subtitle:   i18n.T("web.subtitle"),
		Volumes:    i18n.T("web.volumes"),
		Actions:    i18n.T("web.actions"),
		Sync:       i18n.T("web.sync"),
		Syncing:    i18n.T("web.syncing"),
		SyncDone:   i18n.T("web.sync_done"),
		Config:     i18n.T("web.config"),
		Save:       i18n.T("web.save"),
		Saved:      i18n.T("web.saved"),
		Entries:    i18n.T("web.entries"),
		Used:       i18n.T("web.used"),
		NoDaemon:   i18n.T("web.no_daemon"),
		LoadError:  i18n.T("web.load_error"),
		NewVolume:  i18n.T("web.new_volume"),
		FieldName:  i18n.T("web.field_name"),
		FieldTitle: i18n.T("web.field_title"),
		FieldPoint: i18n.T("web.field_point"),
		Create:     i18n.T("web.create"),
		Creating:   i18n.T("web.creating"),
		Created:    i18n.T("web.created"),
		Restart:    i18n.T("web.restart_hint"),
		AuthLost:   i18n.T("web.auth_lost"),
		NameNeed:   i18n.T("web.name_required"),
		StatVols:   i18n.T("web.stat_volumes"),
		StatFiles:  i18n.T("web.stat_files"),
		StatUsed:   i18n.T("web.stat_used"),
		Xfer:       i18n.T("web.xfer"),
		Footer:     i18n.T("web.footer"),
		Lang:       i18n.Lang(),
		ThemeTip:   i18n.T("web.theme_tip"),
		RefreshTip: i18n.T("web.refresh_tip"),
		CanCreate:  canCreate,
	}
}

// pageData 是整页渲染所需的数据。
type pageData struct {
	Labels pageLabels
	Config string
}

// handlePage 输出内嵌的静态页面。
//
// 输入：canCreate 是否显示建卷卡片；configPath 配置文件路径（空串表示不预载）
// 输出：HTML
// 预期：文案与配置正文都在服务端渲染，前端只负责主题切换
func handlePage(canCreate bool, configPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		data := pageData{Labels: loadLabels(canCreate)}
		if configPath != "" {
			if b, err := os.ReadFile(configPath); err == nil {
				data.Config = string(b)
			}
		}
		if err := pageTmpl.Execute(w, data); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}

// pageTmpl 是内嵌的面板页面模板。
var pageTmpl = template.Must(template.New("page").Funcs(tmplFuncs).Parse(pageHTML))

// handleAsset 输出内嵌的静态资源。
//
// 输入：contentType MIME 类型；data 资源内容
// 输出：HTTP handler
// 预期：内容随二进制固定不变，可长期缓存
func handleAsset(contentType string, data []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		_, _ = w.Write(data)
	}
}

// loadStatus 取当前状态：优先调用方注入，否则回落到 daemon IPC。
//
// 输入：ctx；status 可选的注入实现
// 输出：任意可 JSON 序列化的状态对象；取不到时返回 noDaemon
// 预期：daemon 状态文件残留但进程已退出时也返回 noDaemon，不报 502
func loadStatus(ctx context.Context, status StatusFunc) any {
	if status != nil {
		v, err := status(ctx)
		if err != nil {
			return map[string]any{"error": err.Error()}
		}
		return v
	}
	st, err := daemon.ReadState()
	if err != nil {
		return noDaemon()
	}
	data, err := ipc.Call(ctx, st.Addr, st.Token, "status", nil)
	if err != nil {
		return noDaemon()
	}
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return noDaemon()
	}
	return v
}

// noDaemon 是「没有 daemon 在跑」时的状态。
func noDaemon() map[string]any {
	return map[string]any{
		"state":   i18n.T("web.no_daemon"),
		"volumes": []any{},
	}
}

// handleStatus 输出 JSON 状态，供程序化调用（面板本身走 /frag/status）。
func handleStatus(status StatusFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		v := loadStatus(r.Context(), status)
		if m, ok := v.(map[string]any); ok {
			if e, has := m["error"]; has {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprint(e)})
				return
			}
		}
		writeJSON(w, http.StatusOK, v)
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

// ---- htmx 片段接口 ----
//
// 面板不再手写 fetch 与 DOM 拼接：轮询和提交都打这些接口，服务端直接返回 HTML。
// 状态类片段用 hx-swap-oob 就地替换状态条、统计与卷列表，整页不刷新。

// statusView 是从状态负载归一化出来的渲染视图。
type statusView struct {
	State      string
	Kind       string
	AuthLost   bool
	Volumes    []volumeView
	Transfers  []transferView
	TotalFiles int64
	TotalUsed  int64
	Empty      string
}

// volumeView 是一个卷的展示数据。
type volumeView struct {
	Name    string
	Entries int64
	Used    int64
}

// transferView 是一个正在下载的文件的进度。
type transferView struct {
	Path  string
	Done  int64
	Total int64
	Pct   int
}

// statusFragData 是状态片段的模板数据。
type statusFragData struct {
	Labels pageLabels
	statusView
}

// msgData 是操作反馈片段的模板数据。
//
// Config 非空时一并把配置正文送回页面，省掉一次额外请求。
type msgData struct {
	Class  string
	Text   string
	Config string
}

// rawStatus 对应 /api/status 的 JSON 形状。
//
// 托盘给 volumes/transfers，daemon 只给 entries，两种都兼容。
type rawStatus struct {
	State   string `json:"state"`
	Auth    string `json:"auth"`
	Entries *int64 `json:"entries"`
	Volumes []struct {
		Name    string `json:"name"`
		Entries int64  `json:"entries"`
		Used    int64  `json:"used"`
	} `json:"volumes"`
	Transfers []struct {
		Path  string `json:"path"`
		Done  int64  `json:"done"`
		Total int64  `json:"total"`
	} `json:"transfers"`
}

// viewOf 把任意状态负载转成渲染视图。
//
// 输入：v loadStatus 的返回值；l 当前文案
// 输出：statusView
// 预期：形状不符时退化成空列表并显示「无 daemon」，不 panic、不显示半截数据
func viewOf(v any, l pageLabels) statusView {
	view := statusView{Empty: l.NoDaemon}
	b, err := json.Marshal(v)
	if err != nil {
		return view
	}
	var rs rawStatus
	if err := json.Unmarshal(b, &rs); err != nil {
		return view
	}
	view.State = rs.State
	view.AuthLost = rs.Auth == "lost"
	view.Kind = pillKind(rs.State, view.AuthLost)
	for _, vol := range rs.Volumes {
		view.Volumes = append(view.Volumes, volumeView{Name: vol.Name, Entries: vol.Entries, Used: vol.Used})
		view.TotalFiles += vol.Entries
		view.TotalUsed += vol.Used
	}
	for _, t := range rs.Transfers {
		tv := transferView{Path: t.Path, Done: t.Done, Total: t.Total}
		if t.Total > 0 {
			tv.Pct = int(t.Done * 100 / t.Total)
			if tv.Pct > 100 {
				tv.Pct = 100
			}
		}
		view.Transfers = append(view.Transfers, tv)
	}
	if len(view.Volumes) == 0 && rs.Entries != nil {
		view.Empty = fmt.Sprintf("%s: %d", l.Entries, *rs.Entries)
	}
	return view
}

// pillKind 决定状态条的颜色语义。
//
// 输入：state 状态文本；lost 凭证是否失效
// 输出：ok / warn / bad / 空串
// 预期：凭证失效最优先，其次重连中，最后才是已连接
func pillKind(state string, lost bool) string {
	if lost {
		return "bad"
	}
	lower := strings.ToLower(state)
	switch {
	case strings.Contains(lower, "reconnect"), strings.Contains(lower, "disconnect"),
		strings.Contains(state, "重连"), strings.Contains(state, "断开"):
		return "warn"
	case strings.Contains(lower, "connect"), strings.Contains(lower, "ready"),
		strings.Contains(state, "连接"):
		return "ok"
	}
	return ""
}

// statusFragHTML 是状态片段：全部元素用 hx-swap-oob 替换同 id 的节点。
const statusFragHTML = `<span id="pill" class="pill{{with .Kind}} {{.}}{{end}}" hx-swap-oob="true">{{.State}}</span>
<div id="authbar" class="banner{{if .AuthLost}} show{{end}}" hx-swap-oob="true">{{.Labels.AuthLost}}</div>
<div id="stats" class="stats" hx-swap-oob="true">
<div class="stat"><span class="k">{{.Labels.StatVols}}</span><strong>{{len .Volumes}}</strong></div>
<div class="stat"><span class="k">{{.Labels.StatFiles}}</span><strong>{{.TotalFiles}}</strong></div>
<div class="stat"><span class="k">{{.Labels.StatUsed}}</span><strong>{{bytes .TotalUsed}}</strong></div>
</div>
<div id="volumes" hx-swap-oob="true">{{if .Volumes}}{{range .Volumes}}
<div class="vol"><span class="dot"></span><span class="name">{{.Name}}</span><span class="meta">{{$.Labels.Entries}}: {{.Entries}} · {{$.Labels.Used}}: {{bytes .Used}}</span></div>{{end}}{{else}}
<div class="empty">{{.Empty}}</div>{{end}}{{range .Transfers}}
<div class="vol"><span class="dot busy"></span><span class="name">{{.Path}}</span><span class="meta">{{$.Labels.Xfer}} {{.Pct}}% · {{bytes .Done}} / {{bytes .Total}}</span></div>{{end}}
</div>`

// msgFragHTML 是操作反馈片段。
const msgFragHTML = `<span class="{{.Class}}">{{.Text}}</span>`

// volumeFragHTML 是建卷结果：反馈 + 新配置正文。
const volumeFragHTML = `{{if .Config}}<textarea id="cfg" name="content" spellcheck="false" hx-swap-oob="true">{{.Config}}</textarea>{{end}}<span class="{{.Class}}">{{.Text}}</span>`

var (
	statusFrag = template.Must(template.New("status").Funcs(tmplFuncs).Parse(statusFragHTML))
	msgFrag    = template.Must(template.New("msg").Parse(msgFragHTML))
	volumeFrag = template.Must(template.New("volume").Parse(volumeFragHTML))
)

// renderFragment 输出 HTML 片段。
//
// 输入：w 响应；tmpl 模板；data 模板数据
// 输出：无
// 预期：一律 no-store，避免浏览器缓存旧状态
func renderFragment(w http.ResponseWriter, tmpl *template.Template, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := tmpl.Execute(w, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// handleFragStatus 返回状态片段：状态条、统计、卷列表、下载进度。
func handleFragStatus(status StatusFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		l := loadLabels(false)
		renderFragment(w, statusFrag, statusFragData{
			Labels:     l,
			statusView: viewOf(loadStatus(r.Context(), status), l),
		})
	}
}

// handleFragSync 立即同步一次并返回反馈。
//
// 输入：sync 同步实现，nil 表示当前模式不支持
// 输出：反馈 HTML
// 预期：出错也返回 200 + 错误文案，htmx 默认不替换 4xx/5xx 的响应体
func handleFragSync(sync SyncFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		l := loadLabels(false)
		msg := msgData{Class: "ok", Text: l.SyncDone}
		if sync == nil {
			msg = msgData{Class: "err", Text: i18n.T("web.sync_unavailable")}
		} else if _, err := sync(r.Context()); err != nil {
			msg = msgData{Class: "err", Text: err.Error()}
		}
		renderFragment(w, msgFrag, msg)
	}
}

// handleFragConfig 保存配置正文（表单字段 content）。
func handleFragConfig(path string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		l := loadLabels(false)
		if err := r.ParseForm(); err != nil {
			renderFragment(w, msgFrag, msgData{Class: "err", Text: err.Error()})
			return
		}
		if err := atomic.WriteFile(path, strings.NewReader(r.PostFormValue("content"))); err != nil {
			renderFragment(w, msgFrag, msgData{Class: "err", Text: err.Error()})
			return
		}
		renderFragment(w, msgFrag, msgData{Class: "ok", Text: l.Saved})
	}
}

// handleFragVolume 建卷（表单字段 name/title/point）。
//
// 输出：反馈 HTML；成功时附带改写后的配置正文
// 预期：重名/参数错误都返回 200 + 错误文案，由页面就地显示
func handleFragVolume(create CreateVolumeFunc, configPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		l := loadLabels(true)
		if create == nil {
			renderFragment(w, msgFrag, msgData{Class: "err", Text: i18n.T("web.create_unavailable")})
			return
		}
		if err := r.ParseForm(); err != nil {
			renderFragment(w, msgFrag, msgData{Class: "err", Text: err.Error()})
			return
		}
		name := strings.TrimSpace(r.PostFormValue("name"))
		if name == "" {
			renderFragment(w, msgFrag, msgData{Class: "err", Text: l.NameNeed})
			return
		}
		title := strings.TrimSpace(r.PostFormValue("title"))
		point := strings.TrimSpace(r.PostFormValue("point"))
		if _, err := create(r.Context(), name, title, point); err != nil {
			renderFragment(w, msgFrag, msgData{Class: "err", Text: err.Error()})
			return
		}
		msg := msgData{Class: "ok", Text: l.Created + " · " + l.Restart}
		if configPath != "" {
			if b, err := os.ReadFile(configPath); err == nil {
				msg.Config = string(b)
			}
		}
		renderFragment(w, volumeFrag, msg)
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

const pageHTML = `<!doctype html>
<html lang="{{.Labels.Lang}}" data-theme="dark">
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
.banner.show{display:block}
.ok{color:var(--green)}
.err{color:var(--red)}
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
      <circle cx="221" cy="203" r="5" fill="#3ddc84"/>
    </svg>
    <div>
      <h1>{{.Labels.Title}}</h1>
      <p class="sub">{{.Labels.Subtitle}}</p>
    </div>
    <div class="head-actions">
      <button id="theme" class="ghost iconbtn" type="button" title="{{.Labels.ThemeTip}}" aria-label="{{.Labels.ThemeTip}}">
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
    <div id="authbar" class="banner">{{.Labels.AuthLost}}</div>

    <section class="card">
      <h2>
        <svg class="ic" viewBox="0 0 24 24" aria-hidden="true">
          <line x1="22" x2="2" y1="12" y2="12"/>
          <path d="M5.45 5.11 2 12v6a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2v-6l-3.45-6.89A2 2 0 0 0 16.76 4H7.24a2 2 0 0 0-1.79 1.11Z"/>
          <line x1="6" x2="6.01" y1="16" y2="16"/>
          <line x1="10" x2="10.01" y1="16" y2="16"/>
        </svg>
        <span>{{.Labels.Volumes}}</span>
      </h2>
      <div id="volumes"><div class="empty">…</div></div>
    </section>

    <section class="card">
      <h2>
        <svg class="ic" viewBox="0 0 24 24" aria-hidden="true">
          <path d="M22 12h-4l-3 9L9 3l-3 9H2"/>
        </svg>
        <span>{{.Labels.Actions}}</span>
      </h2>
      <div id="stats" class="stats">
        <div class="stat"><span class="k">{{.Labels.StatVols}}</span><strong>0</strong></div>
        <div class="stat"><span class="k">{{.Labels.StatFiles}}</span><strong>0</strong></div>
        <div class="stat"><span class="k">{{.Labels.StatUsed}}</span><strong>—</strong></div>
      </div>
      <div class="row">
        <button hx-post="/frag/sync" hx-target="#syncmsg" hx-swap="innerHTML" hx-disabled-elt="this">
          <svg class="ic" viewBox="0 0 24 24" aria-hidden="true">
            <path d="M21 12a9 9 0 1 1-9-9c2.52 0 4.85 1.03 6.53 2.7L21 8"/>
            <path d="M21 3v5h-5"/>
          </svg>
          <span>{{.Labels.Sync}}</span>
        </button>
        <button class="ghost iconbtn" type="button" hx-get="/frag/status" hx-swap="none" title="{{.Labels.RefreshTip}}" aria-label="{{.Labels.RefreshTip}}">
          <svg class="ic" viewBox="0 0 24 24" aria-hidden="true">
            <path d="M3 12a9 9 0 1 0 9-9 9.75 9.75 0 0 0-6.74 2.74L3 8"/>
            <path d="M3 3v5h5"/>
          </svg>
        </button>
      </div>
      <p class="msg" id="syncmsg"></p>
    </section>
{{if .Labels.CanCreate}}
    <section class="card" style="grid-column:1/-1">
      <h2>
        <svg class="ic" viewBox="0 0 24 24" aria-hidden="true">
          <path d="M20 20a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.9a2 2 0 0 1-1.69-.9L9.6 3.9A2 2 0 0 0 7.93 3H4a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2Z"/>
          <path d="M12 10v6M9 13h6"/>
        </svg>
        <span>{{.Labels.NewVolume}}</span>
      </h2>
      <form hx-post="/frag/volume" hx-target="#nv-msg" hx-swap="innerHTML">
        <div class="form">
          <input name="name" autocomplete="off" placeholder="{{.Labels.FieldName}}">
          <input name="title" autocomplete="off" placeholder="{{.Labels.FieldTitle}}">
          <input name="point" autocomplete="off" placeholder="{{.Labels.FieldPoint}}">
        </div>
        <div class="row" style="margin-top:12px">
          <button type="submit">
            <svg class="ic" viewBox="0 0 24 24" aria-hidden="true">
              <path d="M12 5v14M5 12h14"/>
            </svg>
            <span>{{.Labels.Create}}</span>
          </button>
          <span class="msg" id="nv-msg"></span>
        </div>
      </form>
    </section>
{{end}}
    <section class="card" style="grid-column:1/-1">
      <h2>
        <svg class="ic" viewBox="0 0 24 24" aria-hidden="true">
          <path d="M12 20h9"/>
          <path d="M16.5 3.5a2.12 2.12 0 0 1 3 3L7 19l-4 1 1-4Z"/>
        </svg>
        <span>{{.Labels.Config}}</span>
      </h2>
      <form hx-put="/frag/config" hx-target="#savemsg" hx-swap="innerHTML">
        <textarea id="cfg" name="content" spellcheck="false">{{.Config}}</textarea>
        <div class="row" style="margin-top:12px">
          <button type="submit">
            <svg class="ic" viewBox="0 0 24 24" aria-hidden="true">
              <path d="M19 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h11l5 5v11a2 2 0 0 1-2 2Z"/>
              <path d="M17 21v-8H7v8M7 3v5h8"/>
            </svg>
            <span>{{.Labels.Save}}</span>
          </button>
          <span class="msg" id="savemsg"></span>
        </div>
      </form>
    </section>
  </main>
  <footer>{{.Labels.Footer}}</footer>
</div>
<div id="poller" hx-get="/frag/status" hx-trigger="load, every 3s" hx-swap="none"></div>
<script src="/static/htmx.min.js"></script>
<script>
// 主题：首屏已在 head 里定好，这里只处理手动切换与系统偏好变更。
// 状态刷新、表单提交全部交给 htmx，页面里没有轮询代码。
(function(){
  var root = document.documentElement;
  var btn = document.getElementById('theme');
  if (!btn) return;
  var paint = function(){
    btn.setAttribute('aria-pressed', root.dataset.theme === 'light' ? 'true' : 'false');
  };
  btn.addEventListener('click', function(){
    root.dataset.theme = root.dataset.theme === 'light' ? 'dark' : 'light';
    try { localStorage.setItem('mytgfs-theme', root.dataset.theme); } catch (e) {}
    paint();
  });
  paint();
  if (window.matchMedia) {
    var mq = window.matchMedia('(prefers-color-scheme: light)');
    var onChange = function(e){
      try { if (localStorage.getItem('mytgfs-theme')) return; } catch (err) {}
      root.dataset.theme = e.matches ? 'light' : 'dark';
      paint();
    };
    if (mq.addEventListener) mq.addEventListener('change', onChange);
    else if (mq.addListener) mq.addListener(onChange);
  }
})();
</script>
</body>
</html>
</html>`
