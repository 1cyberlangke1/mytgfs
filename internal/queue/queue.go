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
	// keepFinished 是持久化保留的已结束任务条数：超出就按 id 删最旧的，
	// 避免 jobs 表随使用时间无限增长。
	keepFinished = 20
	// maxPending 是排队中 + 执行中的任务上限：防止一次批量操作把表撑爆。
	maxPending     = 2000
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

// ErrQueueFull 表示排队任务已达上限。
//
// 输入：无
// 输出：可直接返回的错误
// 预期：批量操作超过上限时立刻报错，而不是把数据库当无限队列
type errQueueFull struct{}

func (errQueueFull) Error() string { return i18n.T("err.queue_full") }

var ErrQueueFull error = errQueueFull{}

// Handler 执行一条任务；必须能从 Job 的字段完全重建操作，不能依赖闭包。
type Handler func(ctx context.Context, j Job) error

// Queue 是持久化串行队列。
type Queue struct {
	db      *sql.DB
	workers int

	mu      sync.RWMutex
	handler Handler
	// onChange 可选：任务入队、开始、结束时各回调一次，供上层推送状态
	onChange func()

	wake []chan struct{}
	// waitMu/waiters 保护任务完成通知：finish 关闭对应通道，Wait 不再轮询
	waitMu   sync.Mutex
	waiters  map[int64][]chan struct{}
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
// 输入：db 本地索引库；workers 并发槽数（<=0 用默认 4）；
// onChange 任务入队/开始/结束时的通知回调（必填，接前端推送）
// 输出：*Queue，尚未启动
// 预期：Open 只做建表与崩溃恢复，调用方必须再调 Start 才会执行任务
func Open(db *sql.DB, workers int, onChange func()) (*Queue, error) {
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
		db:       db,
		workers:  workers,
		waiters:  make(map[int64][]chan struct{}),
		onChange: onChange,
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

// changed 在锁外调用状态变化回调。
//
// 输入：无
// 输出：无
// 预期：回调在 Open 时给定；回调里出错不影响队列
func (q *Queue) changed() {
	q.mu.RLock()
	fn := q.onChange
	q.mu.RUnlock()
	if fn != nil {
		fn()
	}
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
		go q.loop(runCtx, make(chan struct{}, 1))
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
	if err := q.checkCapacity(ctx); err != nil {
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
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	q.signal()
	q.changed()
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
	id, err := q.enqueueOnce(ctx, job)
	if err != nil {
		return 0, err
	}
	q.signal()
	q.changed()
	return id, nil
}

// enqueueOnce 在锁内完成查重与写入，避免并发下同一条任务插两次。
//
// 输入：ctx；job 任务描述
// 输出：任务 ID
// 预期：已有 queued/running 的同参任务时直接复用；否则新建
func (q *Queue) enqueueOnce(ctx context.Context, job Job) (int64, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	var id int64
	err := q.db.QueryRowContext(ctx, `
SELECT id FROM jobs
WHERE volume = ? AND kind = ? AND name = ? AND payload = ? AND state IN (?, ?)
ORDER BY id LIMIT 1`,
		job.Volume, job.Kind, job.Name, job.Payload, stateQueued, stateRunning).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	if err := q.checkCapacity(ctx); err != nil {
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
	return res.LastInsertId()
}

// checkCapacity 确认还能再收一条任务。
//
// 输入：ctx
// 输出：ErrQueueFull 表示排队中 + 执行中已达 maxPending
// 预期：已结束记录不占额度，被保留上限裁掉的旧记录也不会卡住新任务
func (q *Queue) checkCapacity(ctx context.Context) error {
	var n int
	if err := q.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM jobs WHERE state IN (?, ?)`,
		stateQueued, stateRunning).Scan(&n); err != nil {
		return err
	}
	if n >= maxPending {
		return ErrQueueFull
	}
	return nil
}

// ClearFinished 删除所有已结束（成功/失败）的任务记录。
//
// 输入：ctx
// 输出：删除条数
// 预期：排队中与执行中的任务保留；删完推一次状态，面板立即刷新
func (q *Queue) ClearFinished(ctx context.Context) (int64, error) {
	res, err := q.db.ExecContext(ctx,
		`DELETE FROM jobs WHERE state IN (?, ?)`, stateDone, stateFailed)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	q.changed()
	return n, nil
}

// Wait 等待一条任务结束。
//
// 输入：ctx 取消；id 任务 ID
// 输出：done 为 nil；failed 返回任务错误；ctx 取消返回 ctx.Err()
// 预期：任务会被持久化重试，调用方取消只影响本次等待，不影响任务本身
// notifyDone 注册任务完成通知；finish 时关闭通道。
//
// 输入：id 任务 ID
// 输出：通知通道
// 预期：通道只关闭一次；未注册的 id（已被清理）由 Wait 的首查处理
func (q *Queue) notifyDone(id int64) chan struct{} {
	q.waitMu.Lock()
	defer q.waitMu.Unlock()
	ch := make(chan struct{})
	q.waiters[id] = append(q.waiters[id], ch)
	return ch
}

func (q *Queue) notifyDoneClose(id int64) {
	q.waitMu.Lock()
	defer q.waitMu.Unlock()
	for _, ch := range q.waiters[id] {
		close(ch)
	}
	delete(q.waiters, id)
}

// Wait 等待任务到达终态（done/failed）。
//
// 输入：ctx 取消；id 任务 ID
// 输出：done 为 nil；failed 返回任务错误；ctx 取消返回 ctx.Err()
// 预期：finish 关闭通知通道后立刻返回，无轮询；任务记录已被清理（no rows）视为完成
func (q *Queue) Wait(ctx context.Context, id int64) error {
	// 先查一次当前状态：任务可能早已完成或记录已被清理
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

	ch := q.notifyDone(id)
	// 注册后再查一次：finish 可能恰好发生在首查与注册之间
	err = q.db.QueryRowContext(ctx,
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
	case <-ch:
	}

	// 通道关闭后复读终态，避免与 finish 的清理竞态
	err = q.db.QueryRowContext(ctx,
		`SELECT state, err FROM jobs WHERE id = ?`, id).Scan(&state, &msg)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	if state == stateFailed {
		return errors.New(msg)
	}
	return nil
}

// Snapshot 返回最近的任务快照，最新的排在最前。
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

// signal 唤醒所有空闲工作协程。
//
// 输入：无
// 输出：无
// 预期：每个 worker 有自己的 wake 通道，全部非阻塞唤醒；容量 1 保证连发不堆积
func (q *Queue) signal() {
	q.mu.Lock()
	ws := q.wake
	q.mu.Unlock()
	for _, ch := range ws {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// loop 是单个工作协程：领任务、执行、重试。
//
// 预期：自己的 wake 通道注册进 q.wake，退出时注销；signal 广播能立刻唤醒全部空闲 worker
func (q *Queue) loop(ctx context.Context, wake chan struct{}) {
	defer q.wg.Done()
	q.mu.Lock()
	q.wake = append(q.wake, wake)
	q.mu.Unlock()
	defer func() {
		q.mu.Lock()
		for i, ch := range q.wake {
			if ch == wake {
				q.wake = append(q.wake[:i], q.wake[i+1:]...)
				break
			}
		}
		q.mu.Unlock()
	}()
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
			case <-wake:
			case <-ctx.Done():
				return
			}
			continue
		}
		// 开始执行也要推一次：面板要把「排队中」换成「执行中」
		q.changed()
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
	// 结束（成功或失败）必须推一次：这是面板从「执行中」变成最终状态的唯一时点
	q.changed()
	// 唤醒所有等待该任务结束的调用方
	q.notifyDoneClose(id)
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
