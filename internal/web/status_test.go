package web

// 状态视图测试：队列与下载进度都要能进入渲染视图。

import (
	"strings"
	"testing"
)

// TestViewOfQueue 队列负载要转成可渲染的行。
func TestViewOfQueue(t *testing.T) {
	payload := map[string]any{
		"state": "已连接",
		"auth":  "ok",
		"queue": []map[string]any{
			{"name": "demo:a.bin", "state": "running", "attempt": 2, "error": "FLOOD_WAIT"},
		},
	}
	v := viewOf(payload, loadLabels(false))
	if len(v.Queue) != 1 {
		t.Fatalf("队列行数 = %d", len(v.Queue))
	}
	q := v.Queue[0]
	if q.Name != "demo:a.bin" || q.State != "running" || q.Attempt != 2 || q.Err != "FLOOD_WAIT" {
		t.Errorf("队列项 = %+v", q)
	}
}

// TestStatusFragRendersQueue 状态片段里要出现上传队列的卷名前缀。
func TestStatusFragRendersQueue(t *testing.T) {
	v := viewOf(map[string]any{
		"queue": []map[string]any{{"name": "demo:a.bin", "state": "queued"}},
	}, loadLabels(false))
	var b strings.Builder
	if err := statusFrag.Execute(&b, statusFragData{Labels: loadLabels(false), statusView: v}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "demo:a.bin") {
		t.Errorf("片段缺少队列项: %s", b.String())
	}
	if !strings.Contains(b.String(), "上传队列") && !strings.Contains(b.String(), "Upload queue") {
		t.Errorf("片段缺少队列标题: %s", b.String())
	}
}

// TestViewOfTransfersPct 下载进度换算成百分比并封顶。
func TestViewOfTransfersPct(t *testing.T) {
	v := viewOf(map[string]any{
		"transfers": []map[string]any{
			{"path": "demo:big.bin", "done": 50, "total": 200},
			{"path": "demo:over.bin", "done": 500, "total": 100},
		},
	}, loadLabels(false))
	if len(v.Transfers) != 2 {
		t.Fatalf("进度行数 = %d", len(v.Transfers))
	}
	if v.Transfers[0].Pct != 25 {
		t.Errorf("百分比 = %d", v.Transfers[0].Pct)
	}
	if v.Transfers[1].Pct != 100 {
		t.Errorf("超量百分比 = %d", v.Transfers[1].Pct)
	}
}
