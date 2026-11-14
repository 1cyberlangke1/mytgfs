package queue

// 持久化任务队列：所有会改动 Telegram 的操作都先落库，再串行执行。
//
// 为什么落库：断电、杀进程、断网都可能在「请求已发出、结果没记下」的瞬间发生。
// 内存队列一崩就丢，用户看到的是文件没了、目录莫名其妙回退。
// 这里把任务写进本地 SQLite（与索引同一个库），进程重启后 queued/running 的
// 任务会重新排上，瞬时错误（断网、429、5xx）按退避一直重试。
//
// 并发模型：同一个卷严格串行（Telegram 单频道约 1 条消息/秒），不同卷互不阻塞。

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gotd/td/tgerr"

	"mytgfs/internal/i18n"
	"mytgfs/internal/telegramstore"
)

// 任务类型。值会写进数据库，改名需要迁移。
const (
	KindUpload = "upload"
	KindMkdir  = "mkdir"
	KindRename = "rename"
	KindRemove = "remove"
	KindCopy   = "copy"
)

// 任务状态。
const (
	stateQueued  = "queued"
	stateRunning = "running"
	stateDone    = "done"
	stateFailed  = "failed"
)

// 运行参数。
const (
	keepFinished   = 100 // 保留的已结束任务条数
	waitPoll       = 50 * time.Millisecond
	defaultWorkers = 4
	minBackoff     = time.Second
	maxBackoff     = time.Minute
	maxFloodWait   = 15 * time.Minute
)

// Job 是一条待执行任务。
type Job struct {
	ID      int64
	Volume  string
	Kind    string
	Name    string
	Payload string
	Attempt int
}

// Item 是任务快照，供面板与托盘读取。
type Item struct {
	ID      int64  `json:"id"`
	Volume  string `json:"volume"`
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	State   string `json:"state"`
	Attempt int    `json:"attempt"`
	Err     string `json:"error,omitempty"`
}

// Handler 执行一条任务；必须能从 Job 的字段完全重建操作，不能依赖闭包。
type Handler func(ctx context.Context, j Job) error

// Queue 是持久化串行队列。
type Queue struct {
	db      *sql.DB
	workers int

	mu      sync.RWMutex
	handler Handler

	wake     chan struct{}
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	stopOnce sync.Once
}

// schema 是任务表；state+id 建索引因为取队首是热路径。
const schema = `
CREATE TABLE IF NOT EXISTS jobs (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	volume TEXT NOT NULL,
	kind TEXT NOT NULL,
	name TEXT NOT NULL,
	payload TEXT NOT NULL DEFAULT '',
	state TEXT NOT NULL,
	attempt INTEGER NOT NULL DEFAULT 0,
	err TEXT NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_jobs_state ON jobs(state, id);
`

// Open 准备任务表，并把上次进程残留的 running 任务退回排队。
//
// 输入：db 本地索引库；workers 并发槽数（<=0 用默认 4）
// 输出：*Queue，尚未启动
// 预期：Open 只做建表与崩溃恢复，调用方必须再调 Start 才会执行任务
func Open(db *sql.DB, workers int) (*Queue, error) {
	if db == nil {
		return nil, errors.New("queue: nil db")
	}
	if workers <= 0 {
		workers = defaultWorkers
	}
	if _, err := db.Exec(schema); err != nil {
		return nil, err
	}
	// 上次进程在执行中被杀：没有结果可判断，统一重排，交给任务自身做幂等
	if _, err := db.Exec(
		`UPDATE jobs SET state = ?, err = '', updated_at = ? WHERE state = ?`,
		stateQueued, nowUnix(), stateRunning); err != nil {
		return nil, err
	}
	return &Queue{
		db:      db,
		workers: workers,
		wake:    make(chan struct{}, 1),
	}, nil
}

// SetHandler 注册任务执行器。
//
// 输入：fn 按 Kind 分发的执行函数
// 输出：无
// 预期：必须在 Start 之前设置；进程重启后同样的任务会再走一次这个函数
func (q *Queue) SetHandler(fn Handler) {
	q.mu.Lock()
	q.handler = fn
	q.mu.Unlock()
}

// Start 启动工作协程。
//
// 输入：ctx 生命周期，取消即停止领取新任务
// 输出：无
// 预期：可重复调用无副作用；重复调用直接返回
func (q *Queue) Start(ctx context.Context) {
	q.mu.Lock()
	if q.cancel != nil {
		q.mu.Unlock()
		return
	}
	runCtx, cancel := context.WithCancel(ctx)
	q.ctx, q.cancel = runCtx, cancel
	q.mu.Unlock()

	for range q.workers {
		q.wg.Add(1)
		go q.loop(runCtx)
	}
	q.signal()
}

// Close 停止领取任务并等待在跑的任务退出。
//
// 输入：无
// 输出：无
// 预期：在关闭索引库之前调用；在跑任务被取消时保持 running，下次启动自动重排
func (q *Queue) Close() {
	q.stopOnce.Do(func() {
		q.mu.Lock()
		cancel := q.cancel
		q.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		q.signal()
		q.wg.Wait()
	})
}

// Enqueue 持久化一条任务并唤醒工作协程。
//
// 输入：ctx；job 任务描述（Volume/Kind/Name/Payload 必填）
// 输出：任务 ID
// 预期：写库成功才返回；调用方可用 Wait 等待结果，也可直接返回
func (q *Queue) Enqueue(ctx context.Context, job Job) (int64, error) {
	if job.Volume == "" || job.Kind == "" {
		return 0, errors.New("queue: job needs volume and kind")
	}
	now := nowUnix()
	res, err := q.db.ExecContext(ctx, `
INSERT INTO jobs(volume, kind, name, payload, state, attempt, err, created_at, updated_at)
VALUES(?, ?, ?, ?, ?, 0, '', ?, ?)`,
		job.Volume, job.Kind, job.Name, job.Payload, stateQueued, now, now)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	q.signal()
	return id, nil
}

// EnqueueOnce 幂等入队：同卷同类型同名且参数相同的在跑任务只保留一条。
//
// 输入：ctx；job 任务描述
// 输出：任务 ID（已存在时返回已有 ID）
// 预期：启动恢复时 pending 账本与 jobs 表同时存在，靠它避免重复上传
func (q *Queue) EnqueueOnce(ctx context.Context, job Job) (int64, error) {
	if job.Volume == "" || job.Kind == "" {
		return 0, errors.New("queue: job needs volume and kind")
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	var id int64
	err := q.db.QueryRowContext(ctx, `
SELECT id FROM jobs
WHERE volume = ? AND kind = ? AND name = ? AND payload = ? AND state IN (?, ?)
ORDER BY id LIMIT 1`,
		job.Volume, job.Kind, job.Name, job.Payload, stateQueued, stateRunning).Scan(&id)
	if err == nil {
		q.signal()
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	now := nowUnix()
	res, err := q.db.ExecContext(ctx, `
INSERT INTO jobs(volume, kind, name, payload, state, attempt, err, created_at, updated_at)
VALUES(?, ?, ?, ?, ?, 0, '', ?, ?)`,
		job.Volume, job.Kind, job.Name, job.Payload, stateQueued, now, now)
	if err != nil {
		return 0, err
	}
	newID, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	q.signal()
	return newID, nil
}

// Wait 等待一条任务结束。
//
// 输入：ctx 取消；id 任务 ID
// 输出：done 为 nil；failed 返回任务错误；ctx 取消返回 ctx.Err()
// 预期：任务会被持久化重试，调用方取消只影响本次等待，不影响任务本身
func (q *Queue) Wait(ctx context.Context, id int64) error {
	ticker := time.NewTicker(waitPoll)
	defer ticker.Stop()
	for {
		var state, msg string
		err := q.db.QueryRowContext(ctx,
			`SELECT state, err FROM jobs WHERE id = ?`, id).Scan(&state, &msg)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return err
		}
		switch state {
		case stateDone:
			return nil
		case stateFailed:
			return errors.New(msg)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// Snapshot 返回最近的任务快照，按提交顺序。
func (q *Queue) Snapshot() []Item {
	rows, err := q.db.Query(`
SELECT id, volume, kind, name, state, attempt, err FROM jobs
ORDER BY id DESC LIMIT ?`, keepFinished)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := make([]Item, 0, 16)
	for rows.Next() {
		var it Item
		if err := rows.Scan(&it.ID, &it.Volume, &it.Kind, &it.Name, &it.State, &it.Attempt, &it.Err); err != nil {
			return out
		}
		out = append(out, it)
	}
	// 转回升序，面板从上到下就是执行顺序
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// Pending 返回排队中或执行中的任务数。
func (q *Queue) Pending() int {
	var n int
	_ = q.db.QueryRow(
		`SELECT COUNT(*) FROM jobs WHERE state IN (?, ?)`,
		stateQueued, stateRunning).Scan(&n)
	return n
}

// signal 非阻塞唤醒一个工作协程。
func (q *Queue) signal() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

// loop 是单个工作协程：领任务、执行、重试。
func (q *Queue) loop(ctx context.Context) {
	defer q.wg.Done()
	for {
		if ctx.Err() != nil {
			return
		}
		job, ok, err := q.claim(ctx)
		if err != nil {
			slog.Warn(i18n.T("log.queue_claim_failed", err), "err", err)
			if !sleep(ctx, minBackoff) {
				return
			}
			continue
		}
		if !ok {
			select {
			case <-q.wake:
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
			continue
		}
		q.run(ctx, job)
	}
}

// claim 领取一条任务：同卷已有 running 时跳过，实现卷内串行。
func (q *Queue) claim(ctx context.Context) (Job, bool, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	var job Job
	err := q.db.QueryRowContext(ctx, `
SELECT id, volume, kind, name, payload, attempt FROM jobs
WHERE state = ?
  AND volume NOT IN (SELECT volume FROM jobs WHERE state = ?)
ORDER BY id LIMIT 1`, stateQueued, stateRunning).
		Scan(&job.ID, &job.Volume, &job.Kind, &job.Name, &job.Payload, &job.Attempt)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, false, nil
	}
	if err != nil {
		return Job{}, false, err
	}
	res, err := q.db.ExecContext(ctx,
		`UPDATE jobs SET state = ?, attempt = attempt + 1, updated_at = ? WHERE id = ? AND state = ?`,
		stateRunning, nowUnix(), job.ID, stateQueued)
	if err != nil {
		return Job{}, false, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Job{}, false, nil
	}
	job.Attempt++
	return job, true, nil
}

// run 执行任务并按需重试。
func (q *Queue) run(ctx context.Context, job Job) {
	q.mu.RLock()
	handler := q.handler
	q.mu.RUnlock()
	if handler == nil {
		q.finish(job.ID, errors.New(i18n.T("err.queue_no_handler")))
		return
	}
	for {
		err := handler(ctx, job)
		if err == nil {
			q.finish(job.ID, nil)
			return
		}
		// 进程退出导致的中断：不判失败，留给下次启动重排
		if ctx.Err() != nil {
			return
		}
		if !Retryable(err) {
			q.finish(job.ID, telegramstore.DescribeError(err))
			return
		}
		wait := backoff(job.Attempt)
		if d, ok := RetryAfter(err); ok {
			wait = d + time.Second
		}
		slog.Debug(i18n.T("log.queue_retry", job.Name, job.Attempt, err), "task", job.Name, "kind", job.Kind)
		if !sleep(ctx, wait) {
			return
		}
		job.Attempt++
		q.bump(job.ID, job.Attempt)
	}
}

// backoff 计算第 n 次尝试后的等待时间：1s 起翻倍，封顶 1 分钟。
func backoff(attempt int) time.Duration {
	d := minBackoff
	for range max(attempt-1, 0) {
		d *= 2
		if d >= maxBackoff {
			return maxBackoff
		}
	}
	return d
}

// sleep 等待 d，返回 false 表示被取消。
func sleep(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// bump 更新尝试次数，让面板能看到重试进度。
func (q *Queue) bump(id int64, attempt int) {
	_, _ = q.db.Exec(
		`UPDATE jobs SET attempt = ?, updated_at = ? WHERE id = ? AND state = ?`,
		attempt, nowUnix(), id, stateRunning)
}

// finish 记录结果并清理过旧的历史。
func (q *Queue) finish(id int64, err error) {
	state, msg := stateDone, ""
	if err != nil {
		state, msg = stateFailed, err.Error()
	}
	if _, uerr := q.db.Exec(
		`UPDATE jobs SET state = ?, err = ?, updated_at = ? WHERE id = ?`,
		state, msg, nowUnix(), id); uerr != nil {
		slog.Warn(i18n.T("log.queue_finish_failed", uerr), "err", uerr)
		return
	}
	_, _ = q.db.Exec(`
DELETE FROM jobs WHERE state IN (?, ?) AND id NOT IN (
	SELECT id FROM jobs WHERE state IN (?, ?) ORDER BY id DESC LIMIT ?
)`, stateDone, stateFailed, stateDone, stateFailed, keepFinished)
}

// Retryable 判断错误是否值得重试。
//
// 输入：任意 error
// 输出：true 表示可能是瞬时问题（断网、429、5xx）
// 预期：权限/频道/超限/断点失效这类确定性错误重试只会浪费时间
func Retryable(err error) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return false
	case errors.Is(err, telegramstore.ErrTooLarge):
		return false
	case telegramstore.IsChannelUnavailable(err):
		return false
	case telegramstore.IsMessageGone(err):
		return false
	case telegramstore.IsUploadStateError(err):
		return false
	}
	return true
}

// RetryAfter 从 FLOOD_WAIT / 429 里取出服务端要求的等待时间。
//
// 输入：RPC 错误
// 输出：等待时长；ok=false 表示不是限流错误
// 预期：没有具体秒数时按 5 秒处理；上限 15 分钟，避免异常值把队列卡死
func RetryAfter(err error) (time.Duration, bool) {
	e, ok := errors.AsType[*tgerr.Error](err)
	if !ok {
		return 0, false
	}
	secs := 0
	for _, prefix := range []string{"FLOOD_WAIT_", "SLOWMODE_WAIT_", "FLOOD_PREMIUM_WAIT_"} {
		if rest, found := strings.CutPrefix(e.Type, prefix); found {
			if n, perr := strconv.Atoi(rest); perr == nil {
				secs = n
			}
			break
		}
	}
	if secs == 0 && e.Code != 429 {
		return 0, false
	}
	if secs == 0 {
		secs = 5
	}
	d := time.Duration(secs) * time.Second
	return min(d, maxFloodWait), true
}

func nowUnix() int64 { return time.Now().Unix() }
