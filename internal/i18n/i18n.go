package i18n

// 多语言支持：语言包是 embed 的 JSON 文件，文件名即语言标签。
//
// 语言选择优先级：MYTGFS_LANG > 系统语言 > en（回落）。
// 标签匹配依次尝试：完全一致（忽略大小写与 _/- 差异）、同语言的主标签、
// 最后回落到 en。新增一种语言只需要在 locales/ 下加一个 <tag>.json，
// 不需要改代码。

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"
	"sync"
)

//go:embed locales/*.json
var localeFS embed.FS

// locale 是一次加载后的语言包快照。
type locale struct {
	tag  string
	msgs map[string]string
	// fallback 是 en 语言包，用于补齐当前语言缺失的 key
	fallback map[string]string
	// available 是当前二进制里所有可用标签
	available []string
}

// load 只解析一次语言包，之后复用同一份快照。
var load = sync.OnceValue(func() locale {
	return loadFrom(localeFS, detect())
})

// Init 检测并加载语言包；多次调用只生效一次。
func Init() {
	load()
}

// Lang 返回当前语言标签。
func Lang() string {
	return load().tag
}

// Available 返回当前构建里可用的语言标签，按字典序排列。
//
// 输入：无
// 输出：如 ["en", "ja", "zh-CN"]
// 预期：供语言切换菜单与排查用；新增语言包后无需改代码
func Available() []string {
	return append([]string(nil), load().available...)
}

// T 按 key 取翻译。
//
// 输入：key；args 可选，非空时按 fmt.Sprintf 格式化
// 输出：当前语言文本；当前语言缺失时回落到 en；仍然缺失时返回 key
// 预期：不 panic，不显示空串
func T(key string, args ...any) string {
	return tr(load(), key, args...)
}

// tr 在给定快照上取翻译，供 T 与测试共用。
func tr(loc locale, key string, args ...any) string {
	s, ok := loc.msgs[key]
	if !ok {
		s = loc.fallback[key]
	}
	if s == "" {
		return key
	}
	if len(args) == 0 {
		return s
	}
	return fmt.Sprintf(s, args...)
}

// loadFrom 从任意文件系统加载语言包，语言选择逻辑对 embed 与测试通用。
//
// 输入：fsys 含 locales/ 目录的文件系统；want 期望的语言标签
// 输出：locale 快照
// 预期：
//   - want 先做标准匹配，再退到同语言主标签，最后用 en
//   - en 缺失时仍返回空表，T 会回落到 key
func loadFrom(fsys fs.FS, want string) locale {
	available := listLocales(fsys)
	tag := match(available, want)
	if tag == "" {
		tag = "en"
	}
	m, _ := readLocale(fsys, tag)
	fb, _ := readLocale(fsys, "en")
	if m == nil {
		m = map[string]string{}
	}
	if fb == nil {
		fb = map[string]string{}
	}
	return locale{tag: tag, msgs: m, fallback: fb, available: available}
}

// listLocales 列出 locales/ 下所有语言包标签。
//
// 输入：文件系统
// 输出：去掉 .json 的标签，按字典序
// 预期：忽略子目录与非 .json 文件
func listLocales(fsys fs.FS) []string {
	entries, err := fs.ReadDir(fsys, "locales")
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		out = append(out, strings.TrimSuffix(e.Name(), ".json"))
	}
	sort.Strings(out)
	return out
}

// match 在可用标签里找最合适的语言。
//
// 输入：available 可用标签；want 期望标签（如 ja-JP、zh_CN.UTF-8）
// 输出：匹配到的标签；没有可用标签时返回空串
// 预期：先精确匹配，再按主语言匹配；en 作为最终兜底
func match(available []string, want string) string {
	w := normalize(want)
	if w == "" {
		return pickBase(available, "en")
	}
	for _, tag := range available {
		if strings.EqualFold(tag, w) {
			return tag
		}
	}
	base := baseLang(w)
	for _, tag := range available {
		if strings.EqualFold(baseLang(tag), base) {
			return tag
		}
	}
	return pickBase(available, "en")
}

// pickBase 找到指定主语言下的第一个标签。
func pickBase(available []string, base string) string {
	for _, tag := range available {
		if strings.EqualFold(baseLang(tag), base) {
			return tag
		}
	}
	return ""
}

// baseLang 取语言标签的主语言部分。
//
// 输入：如 "zh-CN"、"ja_JP.UTF-8"
// 输出：如 "zh"、"ja"
func baseLang(tag string) string {
	tag = strings.ToLower(strings.TrimSpace(tag))
	tag, _, _ = strings.Cut(tag, ".")
	tag, _, _ = strings.Cut(tag, "_")
	tag, _, _ = strings.Cut(tag, "-")
	return tag
}

// detect 返回要使用的语言标签。
func detect() string {
	if v := os.Getenv("MYTGFS_LANG"); v != "" {
		return normalize(v)
	}
	return normalize(systemLocale())
}

// normalize 把系统语言字符串整理成标签形式。
//
// 输入：如 "zh_CN.UTF-8"、"en-US"、""
// 输出：如 "zh-CN"、"en-US"、""；具体匹配交给 match
func normalize(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	v, _, _ = strings.Cut(v, ".")
	v = strings.ReplaceAll(v, "_", "-")
	parts := strings.Split(v, "-")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p == "" {
			continue
		}
		if len(out) == 0 {
			out = append(out, strings.ToLower(p))
			continue
		}
		out = append(out, strings.ToUpper(p))
	}
	return strings.Join(out, "-")
}

// readLocale 读取并解析语言包。
func readLocale(fsys fs.FS, tag string) (map[string]string, bool) {
	b, err := fs.ReadFile(fsys, "locales/"+tag+".json")
	if err != nil {
		return nil, false
	}
	m := map[string]string{}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, false
	}
	return m, true
}
