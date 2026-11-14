package config

// 面板端口与 config set/get 的单元测试。

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestListenPort 端口默认值与越界回落。
//
// 输入：零值、非法值、合法值
// 输出：监听端口
// 预期：0/负数/超过 65535 都用 DefaultWebPort，正常值原样返回
func TestListenPort(t *testing.T) {
	cases := []struct{ in, want int }{
		{0, DefaultWebPort},
		{-1, DefaultWebPort},
		{70000, DefaultWebPort},
		{18080, 18080},
		{1, 1},
	}
	for _, c := range cases {
		if got := (Web{Port: c.in}).ListenPort(); got != c.want {
			t.Errorf("ListenPort(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

// TestLoadWebSection 解析 [web] 段。
//
// 输入：含 [web] port 的配置
// 输出：Config.Web
// 预期：端口能读到，未写的键保持零值
func TestLoadWebSection(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	body := "[web]\nport = 18090\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Web.Port != 18090 {
		t.Errorf("Port = %d", cfg.Web.Port)
	}
}

// TestSetKeyAppendsAndKeepsComments 只改目标键，其它内容原样保留。
//
// 输入：带注释的配置；set web.port、mount.sync_seconds
// 输出：文件内容
// 预期：注释与其它段落不变；新键写在对应段内；重复设置只替换不重复
func TestSetKeyAppendsAndKeepsComments(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	body := "# 顶部注释\n[telegram]\napi_id = 1\n\n[mount]\nsync_seconds = 15\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetKey(p, "web.port", "18080"); err != nil {
		t.Fatalf("set web.port: %v", err)
	}
	if err := SetKey(p, "mount.sync_seconds", "-1"); err != nil {
		t.Fatalf("set sync_seconds: %v", err)
	}
	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	text := string(got)
	for _, want := range []string{"# 顶部注释", "[telegram]", "api_id = 1", "[mount]", "sync_seconds = -1", "[web]", "port = 18080"} {
		if !contains(text, want) {
			t.Errorf("缺少 %q，内容：\n%s", want, text)
		}
	}
	if got, ok, _ := GetKey(p, "mount.sync_seconds"); !ok || got != "-1" {
		t.Errorf("GetKey sync_seconds = %q ok=%v", got, ok)
	}
	// 重复设置同一个键不能产生第二行
	if err := SetKey(p, "web.port", "18081"); err != nil {
		t.Fatal(err)
	}
	if n := countSubstring(mustRead(t, p), "port = 1808"); n != 1 {
		t.Errorf("port 行数 = %d，应只有一行", n)
	}
}

// TestSetKeyValidates 非法键名与非法取值都要报错。
//
// 输入：未知键、非整数、非布尔
// 输出：error
// 预期：三种情况都返回错误，且不写文件
func TestSetKeyValidates(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	cases := [][2]string{
		{"web.nope", "1"},
		{"web.port", "abc"},
		{"mount.sync_seconds", "abc"},
	}
	for _, c := range cases {
		if err := SetKey(p, c[0], c[1]); err == nil {
			t.Errorf("SetKey(%q, %q) 应报错", c[0], c[1])
		}
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Error("校验失败不应写文件")
	}
}

// TestSetKeyRangeAndEnum 越界与枚举外的值必须被拒绝且给出原因。
//
// 输入：端口 70000、上传并发 99、日志级别 trace
// 输出：error，文件保持原样
// 预期：CLI 与面板共用同一份 validateValue，非法值一条都不落盘
func TestSetKeyRangeAndEnum(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte("[web]\nport = 8720\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := [][2]string{
		{"web.port", "70000"},
		{"mount.upload_parallel", "99"},
		{"log.level", "trace"},
	}
	for _, c := range cases {
		err := SetKey(p, c[0], c[1])
		if err == nil {
			t.Errorf("SetKey(%q, %q) 应被拒绝", c[0], c[1])
			continue
		}
		if strings.TrimSpace(err.Error()) == "" {
			t.Errorf("SetKey(%q, %q) 的错误信息为空，界面无法提示原因", c[0], c[1])
		}
	}
	if got := mustRead(t, p); !strings.Contains(got, "port = 8720") {
		t.Errorf("非法写入改动了文件：\n%s", got)
	}
	// 合法值仍然能写
	if err := SetKey(p, "downloads.dir", "D:/dl"); err != nil {
		t.Fatalf("合法下载目录被拒: %v", err)
	}
	if err := SetKey(p, "log.level", "debug"); err != nil {
		t.Fatalf("合法日志级别被拒: %v", err)
	}
}

// TestSchemaCarriesLimits 面板拿到的 schema 必须带范围与枚举。
func TestSchemaCarriesLimits(t *testing.T) {
	byKey := map[string]SettingInfo{}
	for _, s := range Schema() {
		byKey[s.Key] = s
	}
	if s := byKey["web.port"]; s.Min != 0 || s.Max != 65535 {
		t.Errorf("web.port 范围 = %d..%d", s.Min, s.Max)
	}
	if s := byKey["log.level"]; len(s.Options) != 4 {
		t.Errorf("log.level 枚举 = %v", s.Options)
	}
}

// TestGetKeyMissing 键不存在时返回 ok=false 且不报错。
func TestGetKeyMissing(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte("[mount]\nsync_seconds = 15\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if v, ok, err := GetKey(p, "web.port"); err != nil || ok || v != "" {
		t.Errorf("GetKey = (%q, %v, %v)，期望空值 false nil", v, ok, err)
	}
}

// TestSyncOn 自动同步开关：缺省开启，显式关闭与负数间隔都表示关闭。
//
// 输入：auto_sync 的三种取值 × sync_seconds 的正负
// 输出：SyncOn
// 预期：面板开关与托盘共用同一判断，不会出现 UI 开而实际不同步
func TestSyncOn(t *testing.T) {
	yes, no := true, false
	cases := []struct {
		name string
		m    Mount
		want bool
	}{
		{"缺省视为开", Mount{SyncSeconds: 15}, true},
		{"显式开", Mount{AutoSync: &yes, SyncSeconds: 15}, true},
		{"显式关", Mount{AutoSync: &no, SyncSeconds: 15}, false},
		{"负数间隔优先关", Mount{AutoSync: &yes, SyncSeconds: -1}, false},
	}
	for _, c := range cases {
		if got := c.m.SyncOn(); got != c.want {
			t.Errorf("%s: SyncOn() = %v，期望 %v", c.name, got, c.want)
		}
	}
}

// TestKeysSorted 键列表按字典序返回，方便脚本读取。
func TestKeysSorted(t *testing.T) {
	keys := Keys()
	for i := 1; i < len(keys); i++ {
		if keys[i-1] > keys[i] {
			t.Fatalf("未排序：%v", keys)
		}
	}
	if len(keys) == 0 {
		t.Fatal("键列表为空")
	}
}

func mustRead(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

func countSubstring(s, sub string) int { return strings.Count(s, sub) }

// TestDefaultValueMatchesRuntime 面板缺省值必须等于运行时真实回落。
//
// 输入：零值 Mount；全部可编辑键
// 输出：DefaultValue 与运行时 getter / 常量逐项相等
// 预期：任何一处改默认数字都会让这个测试失败，避免面板显示 0 或与执行行为分叉
func TestDefaultValueMatchesRuntime(t *testing.T) {
	var m Mount
	cases := []struct {
		key  string
		want string
	}{
		{"web.port", strconv.Itoa(DefaultWebPort)},
		{"downloads.dir", DefaultDir},
		{"mount.sync_seconds", strconv.Itoa(DefaultSyncSeconds)},
		{"mount.auto_sync", strconv.FormatBool(m.SyncOn())},
		{"mount.download_parallel", strconv.Itoa(m.DownloadConnections())},
		{"mount.upload_parallel", strconv.Itoa(m.UploadParallelism())},
		{"mount.read_chunk_mb", strconv.Itoa(DefaultReadChunkMB)},
		{"log.level", DefaultLogLevel},
	}
	for _, c := range cases {
		if got := DefaultValue(c.key); got != c.want {
			t.Errorf("DefaultValue(%q) = %q，期望 %q", c.key, got, c.want)
		}
	}
	if got := m.ReadChunk(); got != int64(DefaultReadChunkMB)<<20 {
		t.Errorf("ReadChunk() = %d，期望 %d", got, int64(DefaultReadChunkMB)<<20)
	}
	// 每个可编辑键都必须有默认值，否则面板又会显示空白或 0
	for _, s := range Schema() {
		if strings.TrimSpace(DefaultValue(s.Key)) == "" {
			t.Errorf("键 %q 缺少默认值", s.Key)
		}
	}
}
