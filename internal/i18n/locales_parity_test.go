package i18n

// 语言包一致性：中英文键集合必须完全相同，缺键只在运行时才发现太晚。

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

// TestLocalesParity 两个语言包键集合完全一致且没有空值。
func TestLocalesParity(t *testing.T) {
	zh := loadLocaleFile(t, "zh-CN")
	en := loadLocaleFile(t, "en")

	for k := range zh {
		if _, ok := en[k]; !ok {
			t.Errorf("en 缺少键 %q", k)
		}
	}
	for k := range en {
		if _, ok := zh[k]; !ok {
			t.Errorf("zh-CN 缺少键 %q", k)
		}
	}
	for tag, m := range map[string]map[string]string{"zh-CN": zh, "en": en} {
		for k, v := range m {
			if v == "" {
				t.Errorf("%s.%s 为空", tag, k)
			}
		}
	}
}

// TestPlaceholdersMatch 同一条文案里的 %s/%d/{pct} 占位符数量要一致。
func TestPlaceholdersMatch(t *testing.T) {
	zh := loadLocaleFile(t, "zh-CN")
	en := loadLocaleFile(t, "en")
	for k, zv := range zh {
		ev, ok := en[k]
		if !ok {
			continue
		}
		if countVerb(zv) != countVerb(ev) || countToken(zv, "{pct}") != countToken(ev, "{pct}") {
			t.Errorf("%s 占位符不一致: zh=%q en=%q", k, zv, ev)
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
