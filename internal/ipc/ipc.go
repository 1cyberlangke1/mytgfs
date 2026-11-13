package ipc

// 本地 IPC：TCP loopback + 一行 JSON 请求/响应 + token。
//
// 只绑定 127.0.0.1，token 由 daemon 启动时随机生成并写入 daemon.json。

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sync"
)

// Request 是一行请求。
type Request struct {
	Token  string          `json:"token"`
	Action string          `json:"action"`
	Data   json.RawMessage `json:"data,omitempty"`
}

// Response 是一行响应。
type Response struct {
	OK    bool            `json:"ok"`
	Data  json.RawMessage `json:"data,omitempty"`
	Error string          `json:"error,omitempty"`
}

// Handler 处理一个 action。
type Handler func(ctx context.Context, action string, data json.RawMessage) (any, error)

// Server 是 IPC 服务端。
type Server struct {
	ln      net.Listener
	token   string
	mu      sync.RWMutex
	handler Handler
}

// Listen 在 127.0.0.1 的随机端口上监听。
func Listen(token string) (*Server, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	return &Server{ln: ln, token: token}, nil
}

// Addr 返回监听地址。
func (s *Server) Addr() string { return s.ln.Addr().String() }

// SetHandler 设置 action 处理器。
func (s *Server) SetHandler(h Handler) {
	s.mu.Lock()
	s.handler = h
	s.mu.Unlock()
}

// Serve 接受连接直到 ctx 取消或 Close。
func (s *Server) Serve(ctx context.Context) error {
	// AfterFunc 在 ctx 取消时关监听器，避免 Accept 永久阻塞
	context.AfterFunc(ctx, func() {
		s.ln.Close()
	})
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				return err
			}
		}
		go s.handle(ctx, conn)
	}
}

// Close 关闭监听。
func (s *Server) Close() error { return s.ln.Close() }

func (s *Server) handle(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	enc := json.NewEncoder(conn)

	var req Request
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&req); err != nil {
		_ = enc.Encode(Response{Error: err.Error()})
		return
	}
	if req.Token != s.token {
		_ = enc.Encode(Response{Error: "unauthorized"})
		return
	}

	s.mu.RLock()
	h := s.handler
	s.mu.RUnlock()
	if h == nil {
		_ = enc.Encode(Response{Error: "no handler"})
		return
	}

	v, err := h(ctx, req.Action, req.Data)
	if err != nil {
		_ = enc.Encode(Response{Error: err.Error()})
		return
	}
	b, _ := json.Marshal(v)
	_ = enc.Encode(Response{OK: true, Data: b})
}

// Call 发送一次请求。
//
// 输入：ctx；addr 服务地址；token；action；data 可选
// 输出：响应 data 或 error
// 预期：token 错误、连接失败、action 失败都返回 error
func Call(ctx context.Context, addr, token, action string, data any) (json.RawMessage, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	var raw json.RawMessage
	if data != nil {
		b, err := json.Marshal(data)
		if err != nil {
			return nil, err
		}
		raw = b
	}

	if err := json.NewEncoder(conn).Encode(Request{Token: token, Action: action, Data: raw}); err != nil {
		return nil, err
	}
	var resp Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, fmt.Errorf("%s", resp.Error)
	}
	return resp.Data, nil
}
