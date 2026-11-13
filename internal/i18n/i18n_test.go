package i18n

// 语言选择与回落逻辑的单测：用内存文件系统模拟多语言包。

import (
	"testing"
	"testing/fstest"
)

// testFS 构造三个语言包：en、ja、zh-CN。
func testFS() fstest.MapFS {
	return fstest.MapFS{
		"locales/en.json":    {Data: []byte(`{"hello":"Hello","only_en":"EN"}`)},
		"locales/ja.json":    {Data: []byte(`{"hello":"こんにちは"}`)},
		"locales/zh-CN.json": {Data: []byte(`{"hello":"你好"}`)},
	}
}

// TestMatchLanguage 语言标签匹配：精确、主语言、兜底。
func TestMatchLanguage(t *testing.T) {
	fsys := testFS()
	cases := []struct{ want, tag string }{
		{"ja", "ja"},
		{"ja-JP", "ja"},
		{"ja_JP.UTF-8", "ja"},
		{"zh-CN", "zh-CN"},
		{"zh-TW", "zh-CN"},
		{"en-US", "en"},
		{"fr", "en"},
		{"", "en"},
	}
	for _, c := range cases {
		if got := loadFrom(fsys, c.want).tag; got != c.tag {
			t.Errorf("want %q 得到 %q，期望 %q", c.want, got, c.tag)
		}
	}
}

// TestFallbackToEnglish 当前语言缺 key 时用英文，不显示 key 名。
func TestFallbackToEnglish(t *testing.T) {
	l := loadFrom(testFS(), "ja")
	if got := tr(l, "hello"); got != "こんにちは" {
		t.Errorf("hello = %q", got)
	}
	if got := tr(l, "only_en"); got != "EN" {
		t.Errorf("回落后 = %q，期望 EN", got)
	}
	if got := tr(l, "missing.key"); got != "missing.key" {
		t.Errorf("完全缺失时 = %q，期望 key 本身", got)
	}
}

// TestAvailableListsEveryLocale 可用标签来自文件名，新增语言不改代码。
func TestAvailableListsEveryLocale(t *testing.T) {
	l := loadFrom(testFS(), "en")
	want := []string{"en", "ja", "zh-CN"}
	if len(l.available) != len(want) {
		t.Fatalf("available = %v", l.available)
	}
	for i := range want {
		if l.available[i] != want[i] {
			t.Errorf("available[%d] = %q，期望 %q", i, l.available[i], want[i])
		}
	}
}

// TestEmptyFSDoesNotPanic 连 en 都没有时返回 key，不 panic。
func TestEmptyFSDoesNotPanic(t *testing.T) {
	l := loadFrom(fstest.MapFS{}, "ja")
	if got := tr(l, "hello"); got != "hello" {
		t.Errorf("空语言包时 = %q", got)
	}
	if l.tag != "en" {
		t.Errorf("tag = %q", l.tag)
	}
}
