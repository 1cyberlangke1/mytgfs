package web

// 状态片段的渲染视图与模板常量。
//
// 输入：状态负载（来自 StatusFunc 或托盘注入）
// 输出：statusView 与三个 HTML 片段
// 预期：只做归一化与格式化，不查询任何外部资源

import (
	"encoding/json"
	"html/template"
)

// statusView 是从状态负载归一化出来的渲染视图。
type statusView struct {
	State      string
	Kind       string
	AuthLost   bool
	Volumes    []volumeView
	Transfers  []transferView
	Queue      []queueView
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

// queueView 是一个排队中的上传任务。
type queueView struct {
	Name    string
	State   string
	Attempt int
	Err     string
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
// 卷统计、下载进度、上传队列都由状态提供方填充；缺字段按零值处理。
type rawStatus struct {
	State     string `json:"state"`
	StateCode string `json:"state_code"`
	Auth      string `json:"auth"`
	Volumes   []struct {
		Name    string `json:"name"`
		Entries int64  `json:"entries"`
		Used    int64  `json:"used"`
	} `json:"volumes"`
	Transfers []struct {
		Path  string `json:"path"`
		Done  int64  `json:"done"`
		Total int64  `json:"total"`
	} `json:"transfers"`
	Queue []struct {
		Name    string `json:"name"`
		State   string `json:"state"`
		Attempt int    `json:"attempt"`
		Err     string `json:"error"`
	} `json:"queue"`
}

// viewOf 把任意状态负载转成渲染视图。
//
// 输入：v loadStatus 的返回值；l 当前文案
// 输出：statusView
// 预期：形状不符时退化成空列表并显示空状态文案，不 panic、不显示半截数据
func viewOf(v any, l pageLabels) statusView {
	view := statusView{Empty: l.NoInstance}
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
	view.Kind = pillKind(rs.StateCode, view.AuthLost)
	for _, vol := range rs.Volumes {
		view.Volumes = append(view.Volumes, volumeView{Name: vol.Name, Entries: vol.Entries, Used: vol.Used})
		view.TotalFiles += vol.Entries
		view.TotalUsed += vol.Used
	}
	for _, t := range rs.Transfers {
		tv := transferView{Path: t.Path, Done: t.Done, Total: t.Total}
		if t.Total > 0 {
			tv.Pct = min(int(t.Done*100/t.Total), 100)
		}
		view.Transfers = append(view.Transfers, tv)
	}
	for _, q := range rs.Queue {
		view.Queue = append(view.Queue, queueView{Name: q.Name, State: q.State, Attempt: q.Attempt, Err: q.Err})
	}
	return view
}

// pillKind 决定状态条的颜色语义。
//
// 输入：code 状态代码（online / connecting / reconnecting / auth_lost / ...）；lost 凭证是否失效
// 输出：ok / warn / bad / 空串
// 预期：只看机器可读代码，不匹配翻译后的文本；未知代码保持无色
func pillKind(code string, lost bool) string {
	if lost || code == "auth_lost" {
		return "bad"
	}
	switch code {
	case "reconnecting", "disconnected":
		return "warn"
	case "online", "connecting", "starting", "local", "unmounted":
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
<div class="vol"><span class="dot busy"></span><span class="name">{{.Path}}</span><span class="meta">{{$.Labels.Xfer}} {{.Pct}}% · {{bytes .Done}} / {{bytes .Total}}</span></div>{{end}}{{range .Queue}}
<div class="vol"><span class="dot busy"></span><span class="name">{{.Name}}</span><span class="meta">{{$.Labels.UploadQueue}}: {{.State}}{{if .Attempt}} #{{.Attempt}}{{end}}{{with .Err}} · {{.}}{{end}}</span></div>{{end}}
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
