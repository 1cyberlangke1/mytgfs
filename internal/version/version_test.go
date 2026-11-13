package version

import (
	"runtime"
	"strings"
	"testing"
)

// TestGetFillsRuntime 运行环境字段必须非空且与 runtime 一致。
func TestGetFillsRuntime(t *testing.T) {
	i := Get()
	if i.Version == "" || i.Commit == "" || i.Date == "" {
		t.Errorf("构建字段为空: %+v", i)
	}
	if i.Go != runtime.Version() || i.OS != runtime.GOOS || i.Arch != runtime.GOARCH {
		t.Errorf("运行环境字段不符: %+v", i)
	}
}

// TestStringContainsFields 单行版本串包含版本、提交与平台。
func TestStringContainsFields(t *testing.T) {
	s := String()
	for _, want := range []string{"mytgfs", Version, Commit, runtime.GOOS} {
		if !strings.Contains(s, want) {
			t.Errorf("版本串缺少 %q: %s", want, s)
		}
	}
}
