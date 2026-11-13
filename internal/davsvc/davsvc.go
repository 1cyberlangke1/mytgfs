package davsvc

// 可启停的 WebDAV 服务。
//
// 面板（mytgfs web / 托盘面板）与托盘菜单共用同一份实现：
//   - Start 绑定地址并开始 serve，端口被占用时退到随机端口
//   - Stop 立即关闭监听并删除指向它的网络映射
//   - State 返回快照，供 UI 显示当前地址与卷名

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"golang.org/x/net/webdav"

	"mytgfs/internal/netmap"
)

// State 是服务状态快照。
type State struct {
	Enabled bool
	Addr    string
	Volume  string
}

// Service 管理一个 WebDAV 监听器。
//
// 零值可用；并发安全。Stop 后可以再次 Start。
type Service struct {
	mu     sync.Mutex
	srv    *http.Server
	addr   string
	volume string
}

// Start 在 addr 上提供服务。
//
// 输入：addr 监听地址；volume 卷名（仅用于展示）；fs 文件系统
// 输出：启动后的状态；error
// 预期：
//   - 已在运行则直接返回当前状态，不重复监听
//   - addr 被占用时退到 127.0.0.1:0，返回实际地址
func (s *Service) Start(addr, volume string, fs webdav.FileSystem) (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.srv != nil {
		return s.stateLocked(), nil
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		// 默认端口常被其它软件占用：退到随机端口，实际地址由 UI 展示
		ln, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return State{}, fmt.Errorf("listen %s: %w", addr, err)
		}
	}
	handler := &webdav.Handler{
		Prefix:     "/",
		FileSystem: fs,
		LockSystem: webdav.NewMemLS(),
	}
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	s.srv, s.addr, s.volume = srv, ln.Addr().String(), volume
	go func() {
		_ = srv.Serve(ln)
	}()
	return s.stateLocked(), nil
}

// Stop 立即停止服务并清理网络映射。
//
// 输入：无
// 输出：关闭过程中的错误；未运行时返回 nil
// 预期：先 Shutdown 再删映射，退出后端口不再监听
func (s *Service) Stop() error {
	s.mu.Lock()
	srv, addr := s.srv, s.addr
	s.srv, s.addr, s.volume = nil, "", ""
	s.mu.Unlock()
	if srv == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := srv.Shutdown(ctx)
	netmap.Cleanup(addr)
	return err
}

// State 返回当前状态。
func (s *Service) State() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stateLocked()
}

// stateLocked 组装状态，调用方需持锁。
func (s *Service) stateLocked() State {
	return State{Enabled: s.srv != nil, Addr: s.addr, Volume: s.volume}
}
