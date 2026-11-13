package i18n

// 语言包一致性：所有语言包的键集合必须与 en 完全相同，缺键只在运行时才发现太晚。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// loadLocaleFile 读音标文件为 map。
func loadLocaleFile(t *testing.T, tag string) map[string]string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("locales", tag+".json"))
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]string{}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("%s.json: %v", tag, err)
	}
	return m
}

// localeFiles 列出 locales/ 下所有语言包标签（去掉 .json）。
func localeFiles(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir("locales")
	if err != nil {
		t.Fatal(err)
	}
	var tags []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		tags = append(tags, strings.TrimSuffix(e.Name(), ".json"))
	}
	return tags
}

// TestLocalesParity 每个语言包的键集合与 en 完全一致，且没有空值。
func TestLocalesParity(t *testing.T) {
	tags := localeFiles(t)
	if len(tags) < 2 {
		t.Fatalf("语言包数量 = %d，至少需要 en 与一种翻译", len(tags))
	}
	en := loadLocaleFile(t, "en")
	for tag := range en {
		if en[tag] == "" {
			t.Errorf("en.%s 为空", tag)
		}
	}
	for _, locale := range tags {
		m := loadLocaleFile(t, locale)
		for k := range en {
			if _, ok := m[k]; !ok {
				t.Errorf("%s 缺少键 %q", locale, k)
			}
		}
		for k := range m {
			if _, ok := en[k]; !ok {
				t.Errorf("%s 多出键 %q", locale, k)
			}
			if m[k] == "" {
				t.Errorf("%s.%s 为空", locale, k)
			}
		}
	}
}

// TestPlaceholdersMatch 同一条文案里的 %s/%d/%v/{pct} 占位符数量要一致。
func TestPlaceholdersMatch(t *testing.T) {
	en := loadLocaleFile(t, "en")
	for _, locale := range localeFiles(t) {
		m := loadLocaleFile(t, locale)
		for k, v := range m {
			ev, ok := en[k]
			if !ok {
				continue
			}
			if countVerb(v) != countVerb(ev) || countToken(v, "{pct}") != countToken(ev, "{pct}") {
				t.Errorf("%s.%s 占位符不一致: %q vs en=%q", locale, k, v, ev)
			}
		}
	}
}

// countVerb 统计 fmt 动词 %s/%d/%v 的个数。
func countVerb(s string) int {
	n := 0
	for i := 0; i+1 < len(s); i++ {
		if s[i] == '%' && (s[i+1] == 's' || s[i+1] == 'd' || s[i+1] == 'v') {
			n++
		}
	}
	return n
}

// countToken 统计固定子串出现次数。
func countToken(s, tok string) int {
	return strings.Count(s, tok)
}
