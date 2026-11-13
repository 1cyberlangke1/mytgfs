package config

// 按 "section.key" 读写单个配置项，供 `mytgfs config set/get` 使用。
//
// 只暴露命令行与面板都会用到的键；不在表里的键一律拒绝，
// 避免拼错键名后静默写入无效配置。

import (
	"fmt"
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
	"web.auto_open":           {"web", "auto_open", kindBool},
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
