package ipc

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestRoundTrip(t *testing.T) {
	srv, err := Listen("secret")
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	srv.SetHandler(func(ctx context.Context, action string, data json.RawMessage) (any, error) {
		return map[string]string{"action": action}, nil
	})

	ctx := t.Context()
	go func() { _ = srv.Serve(ctx) }()

	callCtx, callCancel := context.WithTimeout(ctx, 2*time.Second)
	defer callCancel()

	data, err := Call(callCtx, srv.Addr(), "secret", "ping", nil)
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	var got map[string]string
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got["action"] != "ping" {
		t.Fatalf("unexpected: %+v", got)
	}
}

func TestRejectsBadToken(t *testing.T) {
	srv, err := Listen("secret")
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	srv.SetHandler(func(ctx context.Context, action string, data json.RawMessage) (any, error) {
		return "ok", nil
	})

	ctx := t.Context()
	go func() { _ = srv.Serve(ctx) }()

	callCtx, callCancel := context.WithTimeout(ctx, 2*time.Second)
	defer callCancel()

	if _, err := Call(callCtx, srv.Addr(), "wrong", "ping", nil); err == nil {
		t.Fatal("expected unauthorized error")
	}
}
