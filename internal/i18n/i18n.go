package i18n

// 极简 i18n：embed JSON 语言包，按 key 取翻译。
//
// 语言选择优先级：MYTGFS_LANG > 系统语言 > en（回落）。
// 缺失 key 时返回 key 本身，保证程序不会因为翻译缺失崩溃。

import (
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
)

//go:embed locales/*.json
var localeFS embed.FS

var (
	once    sync.Once
	current map[string]string
	langTag string
)

// Init 检测并加载语言包；多次调用只生效一次。
func Init() {
	once.Do(func() {
		tag := detect()
		m, ok := readLocale(tag)
		if !ok {
			tag = "en"
			m, _ = readLocale("en")
		}
		langTag = tag
		current = m
	})
}

// Lang 返回当前语言标签。
func Lang() string {
	Init()
	return langTag
}

// T 按 key 取翻译。
//
// 输入：key；args 可选，非空时按 fmt.Sprintf 格式化
// 输出：翻译文本；key 不存在时返回 key
// 预期：无 args 返回原文，便于 fmt.Errorf 自行格式化
func T(key string, args ...any) string {
	Init()
	s, ok := current[key]
	if !ok {
		return key
	}
	if len(args) == 0 {
		return s
	}
	return fmt.Sprintf(s, args...)
}

// detect 返回要使用的语言标签。
func detect() string {
	if v := os.Getenv("MYTGFS_LANG"); v != "" {
		return normalize(v)
	}
	return normalize(systemLocale())
}

// normalize 把系统语言字符串归一到支持的语言。
//
// 输入：如 "zh_CN.UTF-8"、"en-US"、""
// 输出："zh-CN" 或 "en"
func normalize(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	if strings.HasPrefix(v, "zh") {
		return "zh-CN"
	}
	return "en"
}

// readLocale 读取并解析语言包。
func readLocale(tag string) (map[string]string, bool) {
	b, err := localeFS.ReadFile("locales/" + tag + ".json")
	if err != nil {
		return nil, false
	}
	m := map[string]string{}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, false
	}
	return m, true
}
