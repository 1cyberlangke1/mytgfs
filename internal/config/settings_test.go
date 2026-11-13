package config

// 面板端口与 config set/get 的单元测试。

import (
	"os"
	"path/filepath"
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
	body := "# 顶部注释\n[telegram]\napi_id = 1\n\n[mount]\npoint = \"\"\n"
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
		{"web.auto_open", "maybe"},
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

// TestSetKeyBoolNormalization 布尔输入统一写成 true/false。
//
// 输入：on / off / 1 / 0
// 输出：文件里的字面量
// 预期：都归一化为 TOML 布尔值
func TestSetKeyBoolNormalization(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	for in, want := range map[string]string{"on": "true", "off": "false", "1": "true", "0": "false"} {
		if err := SetKey(p, "webdav.enabled", in); err != nil {
			t.Fatalf("SetKey(%q): %v", in, err)
		}
		if got, ok, _ := GetKey(p, "webdav.enabled"); !ok || got != want {
			t.Errorf("输入 %q 得到 %q ok=%v，期望 %q", in, got, ok, want)
		}
	}
}

// TestSetKeyRangeAndEnum 越界与枚举外的值必须被拒绝且给出原因。
//
// 输入：端口 70000、上传并发 99、日志级别 trace、WebDAV 地址缺端口
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
		{"webdav.addr", "127.0.0.1"},
		{"webdav.addr", "127.0.0.1:99999"},
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
	if err := SetKey(p, "webdav.addr", "127.0.0.1:18080"); err != nil {
		t.Fatalf("合法地址被拒: %v", err)
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
	if err := os.WriteFile(p, []byte("[mount]\npoint = \"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if v, ok, err := GetKey(p, "web.port"); err != nil || ok || v != "" {
		t.Errorf("GetKey = (%q, %v, %v)，期望空值 false nil", v, ok, err)
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
