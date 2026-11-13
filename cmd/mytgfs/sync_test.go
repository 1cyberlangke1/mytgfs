package main

// runSync 的卷解析测试：--name 留空时不能再去碰已删除的旧频道字段。

import (
	"testing"

	"mytgfs/internal/config"
)

// TestResolveVolumeName 显式 --name 优先；留空取第一个卷；没有卷时用默认名。
func TestResolveVolumeName(t *testing.T) {
	withVols := config.Config{Volumes: []config.Volume{
		{Name: "demo", ChatID: 1},
		{Name: "demo2", ChatID: 2},
	}}
	if got := resolveVolumeName(withVols, "demo2"); got != "demo2" {
		t.Fatalf("显式 --name 应原样返回，实际 %q", got)
	}
	if got := resolveVolumeName(withVols, ""); got != "demo" {
		t.Fatalf("留空应取第一个卷，实际 %q", got)
	}
	if got := resolveVolumeName(config.Config{}, ""); got != "mytgfs" {
		t.Fatalf("没有卷配置时应回落到默认名，实际 %q", got)
	}
}
