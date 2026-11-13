package davsvc

import (
	"context"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"golang.org/x/net/webdav"
)

// fakeFS 是空文件系统，够 Start/HTTP 路径使用。
type fakeFS struct{}

func (fakeFS) Mkdir(context.Context, string, os.FileMode) error { return nil }
func (fakeFS) OpenFile(context.Context, string, int, os.FileMode) (webdav.File, error) {
	return nil, os.ErrNotExist
}
func (fakeFS) RemoveAll(context.Context, string) error      { return nil }
func (fakeFS) Rename(context.Context, string, string) error { return nil }
func (fakeFS) Stat(context.Context, string) (os.FileInfo, error) {
	return nil, os.ErrNotExist
}

// freeAddr 取一个当前空闲的端口地址。
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

// TestStartStop 启动后端口可连，停止后不可连。
//
// 用 127.0.0.1:0 让内核分配端口：先探测空闲端口再重新绑定会有竞态，
// 在并发跑测试时偶发被别的进程抢走。
func TestStartStop(t *testing.T) {
	var s Service
	st, err := s.Start("127.0.0.1:0", "demo", fakeFS{})
	if err != nil {
		t.Fatal(err)
	}
	if !st.Enabled || st.Addr == "" || st.Volume != "demo" {
		t.Fatalf("state = %+v", st)
	}
	conn, err := net.DialTimeout("tcp", st.Addr, 2*time.Second)
	if err != nil {
		t.Fatalf("启动后连不上: %v", err)
	}
	_ = conn.Close()

	if err := s.Stop(); err != nil {
		t.Fatal(err)
	}
	if st := s.State(); st.Enabled || st.Addr != "" {
		t.Fatalf("停止后状态 = %+v", st)
	}
	if _, err := net.DialTimeout("tcp", st.Addr, 300*time.Millisecond); err == nil {
		t.Fatal("停止后端口仍在监听")
	}
}

// TestStartTwice 重复 Start 不重复监听，返回同一地址。
func TestStartTwice(t *testing.T) {
	var s Service
	defer s.Stop()
	first, err := s.Start(freeAddr(t), "demo", fakeFS{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Start(freeAddr(t), "other", fakeFS{})
	if err != nil {
		t.Fatal(err)
	}
	if first.Addr != second.Addr || second.Volume != "demo" {
		t.Errorf("第二次 Start 改变了状态: %+v vs %+v", first, second)
	}
}

// TestStopIdempotent 未启动时 Stop 返回 nil。
func TestStopIdempotent(t *testing.T) {
	var s Service
	if err := s.Stop(); err != nil {
		t.Errorf("Stop = %v", err)
	}
}

// TestStartBusyAddrFallsBack 端口占用时退到随机端口。
func TestStartBusyAddrFallsBack(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	var s Service
	defer s.Stop()
	st, err := s.Start(ln.Addr().String(), "demo", fakeFS{})
	if err != nil {
		t.Fatal(err)
	}
	if st.Addr == ln.Addr().String() {
		t.Errorf("应退回随机端口，实际 %s", st.Addr)
	}
}

// TestStartServesHTTP 真实发一个 PROPFIND，确认服务在监听并给出响应。
func TestStartServesHTTP(t *testing.T) {
	var s Service
	defer s.Stop()
	st, err := s.Start(freeAddr(t), "demo", fakeFS{})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest("PROPFIND", "http://"+st.Addr+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 0 {
		t.Error("没有响应码")
	}
}
