package main

// web 模式状态负载的单测：卷统计、缺卷跳过、索引出错只影响该卷。

import (
	"context"
	"testing"

	"mytgfs/internal/config"
	"mytgfs/internal/index"
)

// TestWebStatusSummarizesVolumes 每个已打开的卷都给出条目数与占用。
func TestWebStatusSummarizesVolumes(t *testing.T) {
	ctx := context.Background()
	idx, err := index.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = idx.Close() })

	if err := idx.Put(ctx, index.File{Path: "/a.txt", MessageID: 1, Size: 100}); err != nil {
		t.Fatal(err)
	}
	if err := idx.Put(ctx, index.File{Path: "/b.txt", MessageID: 2, Size: 200}); err != nil {
		t.Fatal(err)
	}

	cfg := config.Config{Volumes: []config.Volume{
		{Name: "demo", ChatID: 11},
		{Name: "missing", ChatID: 22},
	}}
	vols := map[string]*webVolume{"demo": {idx: idx}}

	got := webStatus(ctx, cfg, vols)
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("返回类型 = %T", got)
	}
	if m["state_code"] != "local" {
		t.Errorf("state_code = %v，期望 local", m["state_code"])
	}
	list, ok := m["volumes"].([]map[string]any)
	if !ok || len(list) != 1 {
		t.Fatalf("volumes = %#v，期望只含已打开的卷", m["volumes"])
	}
	if list[0]["name"] != "demo" || list[0]["entries"] != 2 || list[0]["used"] != int64(300) {
		t.Fatalf("卷统计不对: %#v", list[0])
	}
}

// TestWebStatusReportsIndexError 索引读失败时该卷带 error，其余字段仍存在。
func TestWebStatusReportsIndexError(t *testing.T) {
	ctx := context.Background()
	idx, err := index.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	if err := idx.Close(); err != nil {
		t.Fatal(err)
	}

	cfg := config.Config{Volumes: []config.Volume{{Name: "demo", ChatID: 11}}}
	got := webStatus(ctx, cfg, map[string]*webVolume{"demo": {idx: idx}})
	list := got.(map[string]any)["volumes"].([]map[string]any)
	if len(list) != 1 || list[0]["error"] == nil {
		t.Fatalf("期望带 error 的卷记录，得到 %#v", list)
	}
}

// TestWebStatusEmpty 没有任何卷时返回空列表而不是 nil。
func TestWebStatusEmpty(t *testing.T) {
	got := webStatus(context.Background(), config.Config{}, nil)
	m := got.(map[string]any)
	list, ok := m["volumes"].([]map[string]any)
	if !ok || len(list) != 0 {
		t.Fatalf("volumes = %#v，期望空列表", m["volumes"])
	}
}
