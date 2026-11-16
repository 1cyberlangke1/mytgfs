package queue

// 传输层中断的重试用例：断连可重试（有上限），确定性错误仍立即失败。

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"

	"mytgfs/internal/i18n"
)

// TestConnInterruptedRetriesThenSucceeds 引擎掐断连接后自动重试，第二次成功。
func TestConnInterruptedRetriesThenSucceeds(t *testing.T) {
	q := newTestQueue(t, 1)
	var calls atomic.Int32
	q.SetHandler(func(context.Context, Job) error {
		if calls.Add(1) == 1 {
			return context.Canceled
		}
		return nil
	})
	id, err := q.Enqueue(t.Context(), Job{Volume: "demo", Kind: KindUpload, Name: "a.bin"})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if err := q.Wait(t.Context(), id); err != nil {
		t.Fatalf("重试后应成功，实际 %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("handler 调用 %d 次，期望 2", got)
	}
}

// TestConnInterruptedStopsAfterLimit 连接一直断时到上限判失败，不无限重试。
//
// 输入：maxConnRetries 临时设为 0；handler 永远返回 context.Canceled
// 输出：任务 failed 且原因含「断点已保留」
// 预期：只调用一次就停，不进入退避循环
func TestConnInterruptedStopsAfterLimit(t *testing.T) {
	old := maxConnRetries
	maxConnRetries = 0
	t.Cleanup(func() { maxConnRetries = old })

	q := newTestQueue(t, 1)
	var calls atomic.Int32
	q.SetHandler(func(context.Context, Job) error {
		calls.Add(1)
		return context.Canceled
	})
	id, err := q.Enqueue(t.Context(), Job{Volume: "demo", Kind: KindUpload, Name: "a.bin"})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	werr := q.Wait(t.Context(), id)
	if werr == nil {
		t.Fatal("超过上限应当失败")
	}
	if !strings.Contains(werr.Error(), i18n.T("err.queue_conn_lost")) {
		t.Fatalf("错误应带断点提示，实际 %q", werr.Error())
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("handler 调用 %d 次，期望 1", got)
	}
}

// TestDeterministicErrorNotRetried 确定性错误（标记为永久失败）直接失败。
func TestDeterministicErrorNotRetried(t *testing.T) {
	q := newTestQueue(t, 1)
	var calls atomic.Int32
	q.SetHandler(func(context.Context, Job) error {
		calls.Add(1)
		return Permanent(errors.New(i18n.T("err.upload_empty")))
	})
	id, err := q.Enqueue(t.Context(), Job{Volume: "demo", Kind: KindUpload, Name: "a.bin"})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if q.Wait(t.Context(), id) == nil {
		t.Fatal("确定性错误应当失败")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("handler 调用 %d 次，期望 1（不重试）", got)
	}
}

// TestIsConnInterrupted 只把传输层错误认作可重试的连接中断。
func TestIsConnInterrupted(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"canceled", context.Canceled, true},
		{"deadline", context.DeadlineExceeded, true},
		{"eof", io.EOF, true},
		{"unexpected eof", io.ErrUnexpectedEOF, true},
		{"reset", syscall.ECONNRESET, true},
		{"wrapped", errors.Join(errors.New("invoke pool"), context.Canceled), true},
		{"plain", errors.New("boom"), false},
		{"nil", nil, false},
	}
	for _, c := range cases {
		if got := isConnInterrupted(c.err); got != c.want {
			t.Errorf("%s: isConnInterrupted = %v，期望 %v", c.name, got, c.want)
		}
	}
}
