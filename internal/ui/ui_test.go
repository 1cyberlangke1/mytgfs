package ui

import (
	"encoding/json"
	"os"
	"testing"
)

// TestDisabledByDefault 默认不是 JSON 模式。
func TestDisabledByDefault(t *testing.T) {
	if JSONMode() {
		t.Fatal("默认应为文本模式")
	}
}

// TestPrintOnlyInJSONMode 文本模式下不输出 JSON。
func TestPrintOnlyInJSONMode(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	Print(map[string]string{"a": "b"})
	os.Stdout = old
	_ = w.Close()
	buf := make([]byte, 64)
	n, _ := r.Read(buf)
	if n != 0 {
		t.Errorf("文本模式不应输出: %q", buf[:n])
	}
	_ = r.Close()
}

// TestPrintEnvelope JSON 模式输出统一信封。
func TestPrintEnvelope(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	EnableJSON()
	Print(map[string]int{"n": 1})
	os.Stdout = old
	_ = w.Close()

	var got struct {
		OK   bool           `json:"ok"`
		Data map[string]int `json:"data"`
	}
	if err := json.NewDecoder(r).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !got.OK || got.Data["n"] != 1 {
		t.Errorf("信封 = %+v", got)
	}
	_ = r.Close()
}
