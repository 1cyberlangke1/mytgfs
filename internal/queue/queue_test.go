package queue

// 队列单测：用本地 SQLite 临时文件，不联网。
// 覆盖：执行与等待、同卷串行、幂等入队、崩溃重排、瞬时错误重试、确定性错误直接失败、快照顺序。

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/gotd/td/tgerr"

	_ "modernc.org/sqlite"

	"mytgfs/internal/telegramstore"
)

// newTestQueue 打开一个独立库并启动队列，测试结束自动关闭。
//
// 输入：t 测试；workers 并发槽数
// 输出：已 Start 的 *Queue；t.Cleanup 负责 Close 与关库
// 预期：每个用例互不共享库文件，避免状态串味
func newTestQueue(t *testing.T, workers int) *Queue {
	t.Helper()
	// 与 index.Open 同款 pragma：WAL + busy_timeout，否则多协程写会直接 SQLITE_BUSY
	dsn := filepath.Join(t.TempDir(), "queue.db") + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	q, err := Open(db, workers)
	if err != nil {
		t.Fatalf("open queue: %v", err)
	}
	q.Start(t.Context())
	t.Cleanup(func() {
		q.Close()
		_ = db.Close()
	})
	return q
}

// TestEnqueueRunsAndWaits 入队的任务被执行，Wait 在完成后返回 nil。
//
// 输入：一条 upload 任务
// 输出：handler 收到同样的卷/类型/名字；Wait 返回 nil
// 预期：执行结果落成 done，Pending 归零
func TestEnqueueRunsAndWaits(t *testing.T) {
	q := newTestQueue(t, 1)
	got := make(chan Job, 1)
	q.SetHandler(func(_ context.Context, j Job) error {
		got <- j
		return nil
	})

	id, err := q.Enqueue(t.Context(), Job{Volume: "demo", Kind: KindUpload, Name: "a.txt", Payload: `{"x":1}`})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := q.Wait(t.Context(), id); err != nil {
		t.Fatalf("wait: %v", err)
	}

	select {
	case j := <-got:
		if j.Volume != "demo" || j.Kind != KindUpload || j.Name != "a.txt" || j.Payload != `{"x":1}` {
			t.Fatalf("unexpected job: %+v", j)
		}
	default:
		t.Fatal("handler was not called")
	}
	if n := q.Pending(); n != 0 {
		t.Fatalf("pending = %d, want 0", n)
	}
	items := q.Snapshot()
	if len(items) != 1 || items[0].State != stateDone || items[0].Attempt != 1 {
		t.Fatalf("snapshot = %+v", items)
	}
}

// TestSameVolumeSerial 同卷任务不会并发；不同卷可并行。
//
// 输入：demo 两条 + demo2 一条，handler 各阻塞 80ms
// 输出：demo 最大并发 1；总耗时明显小于 3×80ms（说明 demo2 并行）
// 预期：串行只按卷生效，不按全局
func TestSameVolumeSerial(t *testing.T) {
	q := newTestQueue(t, 4)
	var mu sync.Mutex
	running := map[string]int{}
	maxDemo := 0
	q.SetHandler(func(_ context.Context, j Job) error {
		mu.Lock()
		running[j.Volume]++
		if j.Volume == "demo" && running[j.Volume] > maxDemo {
			maxDemo = running[j.Volume]
		}
		mu.Unlock()
		time.Sleep(80 * time.Millisecond)
		mu.Lock()
		running[j.Volume]--
		mu.Unlock()
		return nil
	})

	start := time.Now()
	var ids []int64
	for _, job := range []Job{
		{Volume: "demo", Kind: KindMkdir, Name: "a"},
		{Volume: "demo", Kind: KindMkdir, Name: "b"},
		{Volume: "demo2", Kind: KindMkdir, Name: "c"},
	} {
		id, err := q.Enqueue(t.Context(), job)
		if err != nil {
			t.Fatalf("enqueue: %v", err)
		}
		ids = append(ids, id)
	}
	for _, id := range ids {
		if err := q.Wait(t.Context(), id); err != nil {
			t.Fatalf("wait: %v", err)
		}
	}
	elapsed := time.Since(start)

	mu.Lock()
	defer mu.Unlock()
	if maxDemo != 1 {
		t.Fatalf("demo concurrent = %d, want 1", maxDemo)
	}
	if elapsed >= 240*time.Millisecond {
		t.Fatalf("elapsed = %v, want < 240ms (different volumes run in parallel)", elapsed)
	}
}

// TestEnqueueOnceDedup 同卷同类型同名同参数的在跑任务只保留一条。
//
// 输入：handler 阻塞；连续两次 EnqueueOnce 相同任务
// 输出：两次返回同一个 ID
// 预期：释放后只执行一次
func TestEnqueueOnceDedup(t *testing.T) {
	q := newTestQueue(t, 2)
	release := make(chan struct{})
	var calls int
	var mu sync.Mutex
	q.SetHandler(func(_ context.Context, _ Job) error {
		<-release
		mu.Lock()
		calls++
		mu.Unlock()
		return nil
	})

	job := Job{Volume: "demo", Kind: KindUpload, Name: "a.txt", Payload: "p"}
	first, err := q.EnqueueOnce(t.Context(), job)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := q.EnqueueOnce(t.Context(), job)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if first != second {
		t.Fatalf("ids = %d/%d, want same", first, second)
	}
	close(release)
	if err := q.Wait(t.Context(), first); err != nil {
		t.Fatalf("wait: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
}

// TestRestartRequeuesRunning 上次进程残留的 running 任务在重开时退回排队并执行。
//
// 输入：库里手工插一条 running 任务；Open 后再 Start
// 输出：任务被执行并落成 done
// 预期：崩溃恢复不丢任务
func TestRestartRequeuesRunning(t *testing.T) {
	db, err := sql.Open("sqlite",
		filepath.Join(t.TempDir(), "queue.db")+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	first, err := Open(db, 1)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	_ = first
	now := nowUnix()
	if _, err := db.Exec(
		`INSERT INTO jobs(volume, kind, name, payload, state, attempt, err, created_at, updated_at)
		 VALUES('demo','upload','a.txt','',?,1,'',?,?)`,
		stateRunning, now, now); err != nil {
		t.Fatalf("seed: %v", err)
	}

	q, err := Open(db, 1)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	done := make(chan struct{}, 1)
	q.SetHandler(func(_ context.Context, _ Job) error {
		done <- struct{}{}
		return nil
	})
	q.Start(t.Context())
	defer q.Close()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("requeued job did not run")
	}
}

// TestTransientErrorRetries 瞬时错误会重试，成功后落成 done。
//
// 输入：handler 前两次返回普通错误，第三次成功
// 输出：最终 done，Attempt >= 3
// 预期：断网/429 这类错误不会被判死
func TestTransientErrorRetries(t *testing.T) {
	q := newTestQueue(t, 1)
	var mu sync.Mutex
	attempts := 0
	q.SetHandler(func(_ context.Context, _ Job) error {
		mu.Lock()
		attempts++
		n := attempts
		mu.Unlock()
		if n < 3 {
			return errors.New("network down")
		}
		return nil
	})

	id, err := q.Enqueue(t.Context(), Job{Volume: "demo", Kind: KindUpload, Name: "a.txt"})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := q.Wait(t.Context(), id); err != nil {
		t.Fatalf("wait: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
	items := q.Snapshot()
	if len(items) != 1 || items[0].State != stateDone || items[0].Attempt < 3 {
		t.Fatalf("snapshot = %+v", items)
	}
}

// TestDeterministicErrorFails 确定性错误不重试，直接落 failed 并带原因。
//
// 输入：handler 返回 telegramstore.ErrTooLarge
// 输出：Wait 返回错误；snapshot state=failed 且有 error 文本
// 预期：超限重试一万次也没用
func TestDeterministicErrorFails(t *testing.T) {
	q := newTestQueue(t, 1)
	var mu sync.Mutex
	attempts := 0
	q.SetHandler(func(_ context.Context, _ Job) error {
		mu.Lock()
		attempts++
		mu.Unlock()
		return telegramstore.ErrTooLarge
	})

	id, err := q.Enqueue(t.Context(), Job{Volume: "demo", Kind: KindUpload, Name: "big.bin"})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := q.Wait(t.Context(), id); err == nil {
		t.Fatal("wait returned nil, want error")
	}
	mu.Lock()
	defer mu.Unlock()
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
	items := q.Snapshot()
	if len(items) != 1 || items[0].State != stateFailed || items[0].Err == "" {
		t.Fatalf("snapshot = %+v", items)
	}
}

// TestRetryable 分类瞬时错误与确定性错误。
func TestRetryable(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"canceled", context.Canceled, false},
		{"deadline", context.DeadlineExceeded, false},
		{"too large", telegramstore.ErrTooLarge, false},
		{"network", errors.New("i/o timeout"), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Retryable(c.err); got != c.want {
				t.Fatalf("Retryable(%v) = %v, want %v", c.err, got, c.want)
			}
		})
	}
}

// TestRetryAfter 解析限流错误里的等待时长。
//
// 输入：FLOOD_WAIT_30 / SLOWMODE_WAIT_2 / 429 无类型 / 普通错误
// 输出：对应时长；非限流错误 ok=false
// 预期：429 无秒数按 5 秒；超过 15 分钟被夹住
func TestRetryAfter(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		want   time.Duration
		wantOK bool
	}{
		{"flood", &tgerr.Error{Code: 420, Type: "FLOOD_WAIT_30", Message: "FLOOD_WAIT_30"}, 30 * time.Second, true},
		{"slowmode", &tgerr.Error{Code: 420, Type: "SLOWMODE_WAIT_2", Message: "SLOWMODE_WAIT_2"}, 2 * time.Second, true},
		{"premium", &tgerr.Error{Code: 420, Type: "FLOOD_PREMIUM_WAIT_7", Message: "x"}, 7 * time.Second, true},
		{"429 default", &tgerr.Error{Code: 429, Type: "TOO_MANY_REQUESTS", Message: "x"}, 5 * time.Second, true},
		{"clamped", &tgerr.Error{Code: 420, Type: "FLOOD_WAIT_99999", Message: "x"}, maxFloodWait, true},
		{"other", &tgerr.Error{Code: 400, Type: "CHANNEL_INVALID", Message: "x"}, 0, false},
		{"plain", errors.New("boom"), 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := RetryAfter(c.err)
			if got != c.want || ok != c.wantOK {
				t.Fatalf("RetryAfter = %v/%v, want %v/%v", got, ok, c.want, c.wantOK)
			}
		})
	}
}

// TestSnapshotOrderAndPending 快照按提交升序，Pending 统计排队与执行中。
func TestSnapshotOrderAndPending(t *testing.T) {
	q := newTestQueue(t, 1)
	release := make(chan struct{})
	q.SetHandler(func(_ context.Context, _ Job) error {
		<-release
		return nil
	})

	ids := make([]int64, 0, 3)
	for _, name := range []string{"a", "b", "c"} {
		id, err := q.Enqueue(t.Context(), Job{Volume: "demo", Kind: KindMkdir, Name: name})
		if err != nil {
			t.Fatalf("enqueue: %v", err)
		}
		ids = append(ids, id)
	}
	close(release)
	for _, id := range ids {
		if err := q.Wait(t.Context(), id); err != nil {
			t.Fatalf("wait: %v", err)
		}
	}

	items := q.Snapshot()
	if len(items) != 3 {
		t.Fatalf("items = %d, want 3", len(items))
	}
	for i, want := range []string{"a", "b", "c"} {
		if items[i].Name != want {
			t.Fatalf("items[%d].Name = %q, want %q", i, items[i].Name, want)
		}
	}
	if n := q.Pending(); n != 0 {
		t.Fatalf("pending = %d, want 0", n)
	}
}
