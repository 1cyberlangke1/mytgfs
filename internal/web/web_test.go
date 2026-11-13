package web

// 面板 HTTP 层单测：建卷接口的状态码映射与页面标签注入。

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mytgfs/internal/config"
)

func TestHandleVolumesNotSupported(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/volumes", strings.NewReader(`{"name":"work"}`))
	handleVolumes(nil)(rec, req)
	if rec.Code != http.StatusNotImplemented {
		t.Errorf("code = %d", rec.Code)
	}
}

func TestHandleVolumesRejectsWrongMethod(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/volumes", nil)
	handleVolumes(nil)(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("code = %d", rec.Code)
	}
}

func TestHandleVolumesValidatesInput(t *testing.T) {
	called := false
	fn := func(context.Context, string, string, string) (any, error) {
		called = true
		return nil, nil
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/volumes", strings.NewReader(`not json`))
	handleVolumes(fn)(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad json code = %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/volumes", strings.NewReader(`{"name":"  "}`))
	handleVolumes(fn)(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("empty name code = %d", rec.Code)
	}
	if called {
		t.Error("create should not be called on invalid input")
	}
}

func TestHandleVolumesSuccess(t *testing.T) {
	var gotName, gotTitle, gotPoint string
	fn := func(_ context.Context, name, title, point string) (any, error) {
		gotName, gotTitle, gotPoint = name, title, point
		return map[string]any{"name": name, "muted": true}, nil
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/volumes",
		strings.NewReader(`{"name":"work","title":"工作盘","point":"Y:"}`))
	handleVolumes(fn)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d body = %s", rec.Code, rec.Body.String())
	}
	if gotName != "work" || gotTitle != "工作盘" || gotPoint != "Y:" {
		t.Errorf("args = %q/%q/%q", gotName, gotTitle, gotPoint)
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("json: %v", err)
	}
	if out["muted"] != true {
		t.Errorf("body = %v", out)
	}
}

func TestHandleVolumesConflict(t *testing.T) {
	fn := func(context.Context, string, string, string) (any, error) {
		return nil, config.ErrVolumeExists
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/volumes", strings.NewReader(`{"name":"work"}`))
	handleVolumes(fn)(rec, req)
	if rec.Code != http.StatusConflict {
		t.Errorf("code = %d", rec.Code)
	}
}

func TestPageInjectsLabels(t *testing.T) {
	rec := httptest.NewRecorder()
	handlePage(true, true, "", nil, nil)(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	body := rec.Body.String()
	// 建卷卡片只在支持建卷时渲染
	if !strings.Contains(body, `name="name"`) {
		t.Error("create form missing when volume creation is enabled")
	}
	// 模板必须全部渲染完，不能把 {{...}} 漏给浏览器
	if strings.Contains(body, "{{") {
		t.Error("template placeholder leaked into page")
	}
	// htmx 随二进制分发，页面不能引用外网 CDN
	if !strings.Contains(body, `src="/static/htmx.min.js"`) {
		t.Error("htmx script tag missing")
	}
	if strings.Contains(body, "unpkg.com") || strings.Contains(body, "cdn.") {
		t.Error("page must not depend on external CDN")
	}

	rec = httptest.NewRecorder()
	handlePage(false, false, "", nil, nil)(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	out := rec.Body.String()
	if strings.Contains(out, `name="name"`) {
		t.Error("create form should be absent when volume creation is disabled")
	}
	if strings.Contains(out, "/frag/sync") {
		t.Error("sync button should be absent without a synchronizer")
	}
	if strings.Contains(out, "{{") {
		t.Error("template placeholder leaked into page")
	}
}

// TestPageRendersConfig 配置正文直接在服务端渲染进 textarea，省掉一次请求。
func TestPageRendersConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("[mount]\npoint = \"Y:\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	handlePage(false, false, path, nil, nil)(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if !strings.Contains(rec.Body.String(), `point = &#34;Y:&#34;`) {
		t.Errorf("config not rendered into textarea")
	}
}

// TestFormatBytes 覆盖字节格式化的边界。
func TestFormatBytes(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{1023, "1023 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1 << 20, "1.0 MB"},
		{1 << 30, "1.0 GB"},
		{1 << 40, "1.0 TB"},
	}
	for _, c := range cases {
		if got := formatBytes(c.in); got != c.want {
			t.Errorf("formatBytes(%d) = %q，期望 %q", c.in, got, c.want)
		}
	}
}

// TestPillKind 状态条配色判定。
func TestPillKind(t *testing.T) {
	cases := []struct {
		state string
		lost  bool
		want  string
	}{
		{"已连接", false, "ok"},
		{"连接中", false, "ok"},
		{"重连中", false, "warn"},
		{"disconnected", false, "warn"},
		{"已连接", true, "bad"},
		{"", false, ""},
	}
	for _, c := range cases {
		if got := pillKind(c.state, c.lost); got != c.want {
			t.Errorf("pillKind(%q,%v) = %q，期望 %q", c.state, c.lost, got, c.want)
		}
	}
}

// TestViewOfNormalizes 兼容托盘（volumes/transfers）与 daemon（entries）两种形状。
func TestViewOfNormalizes(t *testing.T) {
	l := pageLabels{NoDaemon: "无 daemon", Entries: "条目", Xfer: "下载中"}

	tray := map[string]any{
		"state": "已连接",
		"auth":  "ok",
		"volumes": []any{
			map[string]any{"name": "demo", "entries": 3, "used": 4096},
			map[string]any{"name": "demo2", "entries": 2, "used": 1024},
		},
		"transfers": []any{
			map[string]any{"path": "/big.bin", "done": 50, "total": 200},
		},
	}
	v := viewOf(tray, l)
	if v.Kind != "ok" || len(v.Volumes) != 2 || v.TotalFiles != 5 || v.TotalUsed != 5120 {
		t.Fatalf("托盘形状归一化失败: %+v", v)
	}
	if len(v.Transfers) != 1 || v.Transfers[0].Pct != 25 {
		t.Fatalf("进度换算失败: %+v", v.Transfers)
	}

	daemon := map[string]any{"entries": float64(7)}
	v = viewOf(daemon, l)
	if v.Empty != "条目: 7" {
		t.Fatalf("daemon 形状应退回条目数，实际 %q", v.Empty)
	}

	if v := viewOf("不是对象", l); v.Empty != "无 daemon" {
		t.Fatalf("非法形状应显示无 daemon，实际 %q", v.Empty)
	}
}

// TestFragStatusSwapsOOB 状态片段用 hx-swap-oob 替换四处节点。
func TestFragStatusSwapsOOB(t *testing.T) {
	status := func(context.Context) (any, error) {
		return map[string]any{
			"state": "已连接",
			"auth":  "ok",
			"volumes": []any{
				map[string]any{"name": "demo", "entries": 3, "used": 4096},
			},
			"transfers": []any{
				map[string]any{"path": "/big.bin", "done": 512, "total": 2048},
			},
		}, nil
	}
	rec := httptest.NewRecorder()
	handleFragStatus(status)(rec, httptest.NewRequest(http.MethodGet, "/frag/status", nil))
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d", rec.Code)
	}
	for _, id := range []string{`id="pill"`, `id="authbar"`, `id="stats"`, `id="volumes"`} {
		if !strings.Contains(body, id) {
			t.Errorf("片段缺少 %s", id)
		}
	}
	if strings.Count(body, `hx-swap-oob="true"`) != 4 {
		t.Errorf("OOB 标记数 = %d，期望 4", strings.Count(body, `hx-swap-oob="true"`))
	}
	// 卷行：名称 + 条目 + 占用
	if !strings.Contains(body, `<span class="name">demo</span>`) || !strings.Contains(body, "12 B") {
		t.Errorf("卷行渲染异常: %s", body)
	}
	// 下载行：进度百分比 + 已下载/总量
	if !strings.Contains(body, `/big.bin`) || !strings.Contains(body, "25%") || !strings.Contains(body, "512 B") {
		t.Errorf("下载行渲染异常: %s", body)
	}
	if strings.Contains(body, "{{") {
		t.Error("模板占位符泄漏")
	}
}

// TestFragStatusWrongMethod 只接受 GET。
func TestFragStatusWrongMethod(t *testing.T) {
	rec := httptest.NewRecorder()
	handleFragStatus(nil)(rec, httptest.NewRequest(http.MethodPost, "/frag/status", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("code = %d", rec.Code)
	}
}

// TestFragSyncReportsError 同步失败也要返回 200 + 错误文案，htmx 才会替换。
func TestFragSyncReportsError(t *testing.T) {
	fn := func(context.Context) (any, error) { return nil, errors.New("boom") }
	rec := httptest.NewRecorder()
	handleFragSync(fn)(rec, httptest.NewRequest(http.MethodPost, "/frag/sync", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "boom") || !strings.Contains(rec.Body.String(), `class="err"`) {
		t.Errorf("body = %s", rec.Body.String())
	}
}

// TestFragSyncUnavailable 未注入同步能力时给出提示。
func TestFragSyncUnavailable(t *testing.T) {
	rec := httptest.NewRecorder()
	handleFragSync(nil)(rec, httptest.NewRequest(http.MethodPost, "/frag/sync", nil))
	if !strings.Contains(rec.Body.String(), `class="err"`) {
		t.Errorf("body = %s", rec.Body.String())
	}
}

// TestFragConfigSavesForm 表单字段 content 写回文件。
func TestFragConfigSavesForm(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	form := url.Values{"content": {"[mount]\npoint = \"Z:\"\n"}}
	req := httptest.NewRequest(http.MethodPut, "/frag/config", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handleFragConfig(path, nil)(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `class="ok"`) {
		t.Fatalf("code = %d body = %s", rec.Code, rec.Body.String())
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != form.Get("content") {
		t.Errorf("文件内容 = %q", got)
	}
}

// TestFragVolumeValidatesName 空卷名不调用建卷。
func TestFragVolumeValidatesName(t *testing.T) {
	called := false
	fn := func(context.Context, string, string, string) (any, error) {
		called = true
		return nil, nil
	}
	req := httptest.NewRequest(http.MethodPost, "/frag/volume", strings.NewReader("name=+"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handleFragVolume(fn, "")(rec, req)
	if called {
		t.Error("空卷名不应调用建卷")
	}
	if !strings.Contains(rec.Body.String(), `class="err"`) {
		t.Errorf("body = %s", rec.Body.String())
	}
}

// TestFragVolumeSendsNewConfig 建卷成功后把新配置正文一起送回，便于就地替换 textarea。
func TestFragVolumeSendsNewConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[[volumes]]\nname = \"new\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fn := func(context.Context, string, string, string) (any, error) {
		return map[string]any{"name": "new"}, nil
	}
	req := httptest.NewRequest(http.MethodPost, "/frag/volume", strings.NewReader("name=new"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handleFragVolume(fn, path)(rec, req)
	body := rec.Body.String()
	if !strings.Contains(body, `id="cfg"`) || !strings.Contains(body, "name = &#34;new&#34;") {
		t.Errorf("新配置未回传: %s", body)
	}
	if !strings.Contains(body, `hx-swap-oob="true"`) {
		t.Error("textarea 缺少 OOB 标记")
	}
}

// TestHTMXEmbedded htmx 必须真的嵌进二进制。
func TestHTMXEmbedded(t *testing.T) {
	if len(htmxJS) < 20000 {
		t.Fatalf("htmx 资源过小: %d 字节", len(htmxJS))
	}
	if !bytes.Contains(htmxJS, []byte("htmx")) {
		t.Error("htmx 资源内容可疑")
	}
}
