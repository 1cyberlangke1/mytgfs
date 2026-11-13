package web

// 事件推送测试：SSE 能收到事件，配置保存会发事件。

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mytgfs/internal/eventbus"
)

// TestEventsStreamsTopic SSE 连接收到对应主题的事件行。
func TestEventsStreamsTopic(t *testing.T) {
	bus := eventbus.New()
	srv := httptest.NewServer(handleEvents(bus))
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %q", ct)
	}

	br := bufio.NewReader(resp.Body)
	// 首帧是注释，确认连接已建立
	line, err := br.ReadString('\n')
	if err != nil || !strings.HasPrefix(line, ":") {
		t.Fatalf("首帧 = %q, %v", line, err)
	}

	bus.Publish("mount")

	// 在限定时间内读到事件名（SSE 会发两行：event 与 data）
	done := make(chan string, 1)
	go func() {
		for {
			l, rerr := br.ReadString('\n')
			if rerr != nil {
				done <- ""
				return
			}
			if after, ok := strings.CutPrefix(l, "event: "); ok {
				done <- strings.TrimSpace(after)
				return
			}
		}
	}()
	select {
	case got := <-done:
		if got != "mount" {
			t.Errorf("事件名 = %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("没有收到事件")
	}
}

// TestFragConfigPublishes 保存配置后要通知其它页面。
func TestFragConfigPublishes(t *testing.T) {
	bus := eventbus.New()
	ch, cancel := bus.Subscribe("config")
	defer cancel()

	p := filepath.Join(t.TempDir(), "config.toml")
	req := httptest.NewRequest(http.MethodPut, "/frag/config", strings.NewReader("content=hello"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handleFragConfig(p, bus)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d", rec.Code)
	}
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("没有发出 config 事件")
	}
	if b, err := os.ReadFile(p); err != nil || string(b) != "hello" {
		t.Errorf("文件内容 = %q, %v", b, err)
	}
}

// TestFragMountGetDoesNotToggle GET 只读状态，不能改动开关。
func TestFragMountGetDoesNotToggle(t *testing.T) {
	var calls []bool
	rec := httptest.NewRecorder()
	handleFragMount(newTestMount(&calls, nil), nil)(rec, httptest.NewRequest(http.MethodGet, "/frag/mount", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d", rec.Code)
	}
	if len(calls) != 0 {
		t.Errorf("GET 不应触发启停: %v", calls)
	}
	if !strings.Contains(rec.Body.String(), `id="davaddr"`) {
		t.Errorf("缺少地址节点: %s", rec.Body.String())
	}
}

// TestFragMountPublishes 启停成功后要发 mount 事件。
func TestFragMountPublishes(t *testing.T) {
	bus := eventbus.New()
	ch, cancel := bus.Subscribe("mount")
	defer cancel()
	var calls []bool
	rec := httptest.NewRecorder()
	handleFragMount(newTestMount(&calls, nil), bus)(rec, formRequest("/frag/mount", "on=1"))
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("没有发出 mount 事件")
	}
}
