package config

// 按 "section.key" 读写单个配置项，供 `mytgfs config set/get` 使用。
//
// 只暴露命令行与面板都会用到的键；不在表里的键一律拒绝，
// 避免拼错键名后静默写入无效配置。

import (
	"fmt"
	"net"
	"os"
	"slices"
	"strconv"
	"strings"

	"mytgfs/internal/i18n"
)

// settingKind 是配置项的取值类型。
type settingKind int

const (
	kindString settingKind = iota
	kindInt
	kindBool
)

// setting 描述一个可读写的键。
type setting struct {
	section string
	key     string
	kind    settingKind
}

// settings 是允许通过命令行修改的键（"section.key" => 描述）。
var settings = map[string]setting{
	"web.port":                {"web", "port", kindInt},
	"webdav.enabled":          {"webdav", "enabled", kindBool},
	"webdav.addr":             {"webdav", "addr", kindString},
	"webdav.volume":           {"webdav", "volume", kindString},
	"pwa.enabled":             {"pwa", "enabled", kindBool},
	"pwa.name":                {"pwa", "name", kindString},
	"pwa.offline_max_mb":      {"pwa", "offline_max_mb", kindInt},
	"mount.sync_seconds":      {"mount", "sync_seconds", kindInt},
	"mount.download_parallel": {"mount", "download_parallel", kindInt},
	"mount.read_chunk_mb":     {"mount", "read_chunk_mb", kindInt},
	"mount.upload_parallel":   {"mount", "upload_parallel", kindInt},
	"log.level":               {"log", "level", kindString},
}

// SettingInfo 描述一个可读写键给界面用。
type SettingInfo struct {
	Key     string   `json:"key"`
	Kind    string   `json:"kind"` // string | int | bool
	Min     int      `json:"min,omitempty"`
	Max     int      `json:"max,omitempty"`
	Options []string `json:"options,omitempty"`
}

// settingLimits 是整数键的合法范围（含端点）。
//
// 与各键的运行时夹取范围保持一致：调大只会被夹回，不如直接拒绝写入。
var settingLimits = map[string]struct{ min, max int }{
	"web.port":                {0, 65535},
	"mount.sync_seconds":      {-1, 86400},
	"mount.download_parallel": {0, 8},
	"mount.upload_parallel":   {0, 16},
	"mount.read_chunk_mb":     {0, 64},
	"pwa.offline_max_mb":      {-1, 1048576},
}

// settingOptions 是枚举键的合法取值。
var settingOptions = map[string][]string{
	"log.level": {"debug", "info", "warn", "error"},
}

// validateValue 校验一个键的取值。
//
// 输入：key 键名；raw 已规范化的字面量（如 "true"、"8080"、`"127.0.0.1:8080"`）
// 输出：error；不合法时带「为什么非法」的可读原因
// 预期：
//   - CLI 的 config set 与面板 PUT 共用这一份规则
//   - 范围键拒绝越界，枚举键拒绝未知值，端口/地址拒绝非法格式
func validateValue(key, raw string) error {
	v := strings.Trim(strings.TrimSpace(raw), `"`)
	if lim, ok := settingLimits[key]; ok {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf(i18n.T("err.config_int_format"), raw)
		}
		if n < lim.min || n > lim.max {
			return fmt.Errorf(i18n.T("err.config_range"), key, lim.min, lim.max, n)
		}
		return nil
	}
	if opts, ok := settingOptions[key]; ok {
		for _, o := range opts {
			if v == o {
				return nil
			}
		}
		return fmt.Errorf(i18n.T("err.config_enum"), key, strings.Join(opts, " / "), v)
	}
	switch key {
	case "webdav.addr":
		if v == "" {
			return nil
		}
		if err := checkHostPort(v); err != nil {
			return fmt.Errorf(i18n.T("err.config_addr"), v)
		}
	case "pwa.name", "webdav.volume":
		if len([]rune(v)) > 64 {
			return fmt.Errorf(i18n.T("err.config_too_long"), key, 64)
		}
	}
	return nil
}

// checkHostPort 校验 host:port 形式，端口必须在 1..65535。
//
// 输入：addr 形如 127.0.0.1:8080
// 输出：error；缺端口或端口越界时非 nil
// 预期：允许主机名与 IPv4，不做 DNS 解析
func checkHostPort(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil || strings.TrimSpace(host) == "" {
		return fmt.Errorf("bad host:port")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("bad port")
	}
	return nil
}

// Schema 返回全部可读写键及其类型、范围与枚举，按字典序排列。
//
// 输入：无
// 输出：键名、类型、整数范围、枚举取值
// 预期：面板据此动态渲染表单并做前端预校验，规则与 validateValue 同源
func Schema() []SettingInfo {
	out := make([]SettingInfo, 0, len(settings))
	for k, s := range settings {
		kind := "string"
		switch s.kind {
		case kindInt:
			kind = "int"
		case kindBool:
			kind = "bool"
		}
		info := SettingInfo{Key: k, Kind: kind}
		if lim, ok := settingLimits[k]; ok {
			info.Min, info.Max = lim.min, lim.max
		}
		if opts, ok := settingOptions[k]; ok {
			info.Options = opts
		}
		out = append(out, info)
	}
	slices.SortFunc(out, func(a, b SettingInfo) int { return strings.Compare(a.Key, b.Key) })
	return out
}

// Keys 返回所有可读写键，按字典序排列。
//
// 输入：无
// 输出：形如 "web.port" 的键名列表
// 预期：供 CLI 报错时列出可用键
func Keys() []string {
	out := make([]string, 0, len(settings))
	for k := range settings {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// SetKey 写入单个配置项。
//
// 输入：path 配置文件；name "section.key"；value 原样字符串（bool 接受 true/false）
// 输出：error；未知键或取值不合法时返回错误
// 预期：
//   - 只改这一个键，其它段落、注释、未知键原样保留
//   - bool 统一写成 true/false；int 校验为十进制整数
//   - 段落不存在时追加
func SetKey(path, name, value string) error {
	s, ok := settings[name]
	if !ok {
		return fmt.Errorf(i18n.T("err.config_key_unknown"), name, strings.Join(Keys(), ", "))
	}
	lit, err := normalizeValue(s.kind, value)
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	if err := validateValue(name, lit); err != nil {
		return err
	}
	return rewriteSection(path, s.section, []string{s.key + " = " + lit}, nil)
}

// GetKey 读取单个配置项。
//
// 输入：path 配置文件；name "section.key"
// 输出：值（已去引号）、是否存在、error
// 预期：文件或段落或键不存在时返回 ok=false，不报错
func GetKey(path, name string) (string, bool, error) {
	s, ok := settings[name]
	if !ok {
		return "", false, fmt.Errorf(i18n.T("err.config_key_unknown"), name, strings.Join(Keys(), ", "))
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, err
	}
	section := ""
	for line := range strings.SplitSeq(string(raw), "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") {
			section = strings.TrimSpace(strings.Trim(t, "[]"))
			continue
		}
		if section != s.section {
			continue
		}
		k, v := splitKeyValue(line)
		if k == s.key {
			return strings.Trim(v, `"'`), true, nil
		}
	}
	return "", false, nil
}

// normalizeValue 把用户输入统一成 TOML 字面量。
//
// 输入：kind 类型；value 用户输入
// 输出：可直接写进配置的字面量
// 预期：bool 只接受 true/false（大小写不敏感）；int 必须是十进制整数；
// string 用 TOML 双引号转义
func normalizeValue(kind settingKind, value string) (string, error) {
	v := strings.TrimSpace(value)
	switch kind {
	case kindBool:
		switch strings.ToLower(v) {
		case "true", "1", "on", "yes":
			return "true", nil
		case "false", "0", "off", "no":
			return "false", nil
		}
		return "", fmt.Errorf(i18n.T("err.config_bool_format"), value)
	case kindInt:
		n, err := strconv.Atoi(v)
		if err != nil {
			return "", fmt.Errorf(i18n.T("err.config_int_format"), value)
		}
		return strconv.Itoa(n), nil
	default:
		return tomlString(v), nil
	}
}
