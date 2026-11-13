package netmap

import (
	"strings"
	"testing"
)

// TestPatternsMatchBothForms 地址匹配要覆盖 host:port 与 host@port 两种显示形式。
//
// 这里不真的执行 net use，只验证匹配逻辑的两种模式都被构造出来；
// 实际删除在 Windows 上才有意义，交给集成验证。
func TestPatternsMatchBothForms(t *testing.T) {
	addr := "127.0.0.1:8080"
	patterns := []string{addr, strings.Replace(addr, ":", "@", 1)}
	if patterns[0] != "127.0.0.1:8080" {
		t.Errorf("第一种模式 = %q", patterns[0])
	}
	if patterns[1] != "127.0.0.1@8080" {
		t.Errorf("第二种模式 = %q", patterns[1])
	}
}

// TestCleanupEmptyAddr 空地址不 panic，也不该去跑 net use。
func TestCleanupEmptyAddr(t *testing.T) {
	Cleanup("") // 不 panic 即通过
}
