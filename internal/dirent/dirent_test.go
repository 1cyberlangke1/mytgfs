package dirent

// 节点 body 的编解码单测：往返一致、非法输入拒绝、名字长度上限。

import (
	"errors"
	"strings"
	"testing"
)

// TestEncodeParseFile 文件节点往返。
func TestEncodeParseFile(t *testing.T) {
	want := Node{Name: "a.txt", Parent: 42, Sha: "abc123"}
	body, err := Encode(want)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, ok := Parse(body)
	if !ok {
		t.Fatalf("Parse 失败: %s", body)
	}
	if got != want {
		t.Fatalf("往返=(%+v) want(%+v)", got, want)
	}
}

// TestEncodeParseDir 目录节点往返，且不带摘要。
func TestEncodeParseDir(t *testing.T) {
	want := Node{Name: "docs", Parent: 0, Dir: true}
	body, err := Encode(want)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if strings.Contains(body, `"s"`) {
		t.Fatalf("目录 body 不该有摘要: %s", body)
	}
	got, ok := Parse(body)
	if !ok || got != want {
		t.Fatalf("往返=(%+v,%v) want(%+v)", got, ok, want)
	}
}

// TestParseRejectsLegacy 旧路径格式与坏 JSON 一律拒绝。
func TestParseRejectsLegacy(t *testing.T) {
	for _, s := range []string{
		`{"path":"/a.txt","sha256":"x"}`,
		`{"v":1,"t":"f","n":"a.txt"}`,
		`{"v":2,"t":"r","n":"a"}`,
		`{"v":2,"t":"c","n":"a"}`,
		`not json`,
		``,
	} {
		if _, ok := Parse(s); ok {
			t.Fatalf("Parse(%q) 应该失败", s)
		}
	}
}

// TestEncodeRejectsBadName 空名、点名字、含分隔符的名字都不能编码。
func TestEncodeRejectsBadName(t *testing.T) {
	for _, name := range []string{"", ".", "..", "a/b", `a\b`, "a\x00b"} {
		if _, err := Encode(Node{Name: name}); err == nil {
			t.Fatalf("Encode(%q) 应该失败", name)
		}
	}
}

// TestEncodeRejectsNegativeParent 负父消息号不能编码。
func TestEncodeRejectsNegativeParent(t *testing.T) {
	if _, err := Encode(Node{Name: "a", Parent: -1}); err == nil {
		t.Fatal("负父消息号应该失败")
	}
}

// TestNameLengthLimit 名字字节上限：恰好等于上限可编码，超过报 ErrNameTooLong。
func TestNameLengthLimit(t *testing.T) {
	ok := strings.Repeat("a", MaxNameBytes)
	if _, err := Encode(Node{Name: ok}); err != nil {
		t.Fatalf("恰好 %d 字节应该可编码: %v", MaxNameBytes, err)
	}
	tooLong := strings.Repeat("a", MaxNameBytes+1)
	if _, err := Encode(Node{Name: tooLong}); !errors.Is(err, ErrNameTooLong) {
		t.Fatalf("超过上限应返回 ErrNameTooLong，实际 %v", err)
	}
}

// TestParseRejectsLongName 远端数据里超长名字也不解析，防止拼出超长路径。
func TestParseRejectsLongName(t *testing.T) {
	body := `{"v":2,"t":"f","n":"` + strings.Repeat("a", MaxNameBytes+1) + `"}`
	if _, ok := Parse(body); ok {
		t.Fatal("超长名字不该解析成功")
	}
}

// TestEncodeBodySize 限长后 body 远小于 caption 上限，不需要元信息替身。
func TestEncodeBodySize(t *testing.T) {
	body, err := Encode(Node{Name: strings.Repeat("a", MaxNameBytes), Parent: 999999999, Sha: strings.Repeat("b", 64)})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if len(body) > CaptionLimit {
		t.Fatalf("body 长度 %d 超过 caption 上限 %d", len(body), CaptionLimit)
	}
}
