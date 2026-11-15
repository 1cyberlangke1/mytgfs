package logging

import (
	"log/slog"
	"testing"
)

// 回归背景：日志级别启动时定死，面板改 log.level 不生效。
// SetLevel 必须能在运行期切换全局级别。
func TestSetLevelSwitches(t *testing.T) {
	logLevel.Set(ParseLevel("warn"))
	if got := logLevel.Level(); got != slog.LevelWarn {
		t.Fatalf("级别 = %v; want warn", got)
	}
	SetLevel("debug")
	if got := logLevel.Level(); got != slog.LevelDebug {
		t.Fatalf("级别 = %v; want debug", got)
	}
	// 未知名字回落 Info
	SetLevel("nonsense")
	if got := logLevel.Level(); got != slog.LevelInfo {
		t.Fatalf("级别 = %v; want info", got)
	}
}
