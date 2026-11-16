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
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"syscall"
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
//
// 输入：无
// 输出：持久化与接口里出现的状态字符串
// 预期：面板按这些字符串渲染，改动会同时影响两边
const (
	StateQueued  = "queued"
	StateRunning = "running"
	StateDone    = "done"
	StateFailed  = "failed"
)

const (
	stateQueued  = StateQueued
	stateRunning = StateRunning
	stateDone    = StateDone
	stateFailed  = StateFailed
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
	// Done 是已推进的字节数，Size 是总字节数；未知时为 0。
	// 只有执行中的任务会上报，面板据此显示真实进度而不是 0/100
	Done int64 `json:"done,omitempty"`
	Size int64 `json:"size,omitempty"`
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

	// wakes 是每个 worker 一个的唤醒通道：Open 时按 workers 固定长度创建，
	// 之后只读不增删，signal 广播不会与 worker 生命周期竞态
	wakes []chan struct{}
	// waitMu/waiters 保护任务完成通知：finish 关闭对应通道，Wait 不再轮询
	waitMu   sync.Mutex
	waiters  map[int64][]chan struct{}
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	stopOnce sync.Once

	// progMu/progress 记录执行中任务的字节进度：键是任务 id。
	// 只放内存、不落库：重启后任务重排，进度从 0 重新上报即可
	progMu   sync.Mutex
	progress map[int64]progress
}

// progress 是一条任务的字节进度。
type progress struct {
	done int64
	size int64
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
	// 每个 worker 一个容量 1 的唤醒通道：make([]chan, n) 得到的是 nil 通道，
	// 必须逐个 make，否则 signal 的非阻塞发送全部落空，空闲 worker 睡死
	wakes := make([]chan struct{}, workers)
	for i := range wakes {
		wakes[i] = make(chan struct{}, 1)
	}
	return &Queue{
		db:       db,
		workers:  workers,
		wakes:    wakes,
		waiters:  make(map[int64][]chan struct{}),
		progress: make(map[int64]progress),
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

	for i := range q.wakes {
		q.wg.Add(1)
		go q.loop(runCtx, q.wakes[i])
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

// Report 记录执行中任务的字节进度并推一次状态。
//
// 输入：id 任务 id；done 已完成字节；size 总字节
// 输出：无
// 预期：handler 在执行过程中调用；只更新内存，任务结束即删除；
//
//	done 单调不减，size 取最后一次非零值
func (q *Queue) Report(id, done, size int64) {
	if id == 0 || size <= 0 {
		return
	}
	if done < 0 {
		done = 0
	}
	if done > size {
		done = size
	}
	q.progMu.Lock()
	prev, ok := q.progress[id]
	if ok && done < prev.done && prev.size == size {
		// 续传回到早期分片时会报更小的值：进度条不能倒退
		done = prev.done
	}
	q.progress[id] = progress{done: done, size: size}
	q.progMu.Unlock()
	q.changed()
}

// progressOf 读取任务进度。
//
// 输入：id 任务 id
// 输出：done 与 size；没有记录时为 0
func (q *Queue) progressOf(id int64) (int64, int64) {
	q.progMu.Lock()
	p, ok := q.progress[id]
	q.progMu.Unlock()
	if !ok {
		return 0, 0
	}
	return p.done, p.size
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
		it.Done, it.Size = q.progressOf(it.ID)
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
// 预期：wakes 在 Open 时固定创建、只读遍历，无锁无竞态；容量 1 保证连发不堆积
func (q *Queue) signal() {
	for _, ch := range q.wakes {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// loop 是单个工作协程：领任务、执行、重试。
//
// 预期：wake 通道来自 Open 时固定创建的 q.wakes，本协程退出即不再接收唤醒
func (q *Queue) loop(ctx context.Context, wake chan struct{}) {
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

// uploadLane 是同一卷内允许同时推进的上传任务数。
//
// 输入：无
// 输出：同卷并发上传上限
// 预期：每个上传任务内部本来就有 upload_parallel 路分片；再叠 3 个任务，
//
//	是为了让「每个文件固定的几次往返」重叠起来，而不是按某次测速结果调参；
//	频道消息速率仍由 Store 的 1 msg/s 限流兜住，不随这里放大
const uploadLane = 3

// claim 领取一条任务。
//
// 输入：ctx
// 输出：任务、是否领到、错误
// 预期：同卷的元数据操作仍然互斥；同卷上传最多 uploadLane 个并行，
//
//	让多个小文件的固定往返重叠，而不会把频道写操作堆在一起
func (q *Queue) claim(ctx context.Context) (Job, bool, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	// 先看正在跑的任务：按卷统计总在跑数与上传数
	rows, err := q.db.QueryContext(ctx,
		`SELECT volume, kind, COUNT(*) FROM jobs WHERE state = ? GROUP BY volume, kind`, stateRunning)
	if err != nil {
		return Job{}, false, err
	}
	running := make(map[string]int)   // 卷内正在跑的任务数
	uploading := make(map[string]int) // 卷内正在跑的上传任务数
	for rows.Next() {
		var vol, kind string
		var n int
		if err := rows.Scan(&vol, &kind, &n); err != nil {
			rows.Close()
			return Job{}, false, err
		}
		running[vol] += n
		if kind == "upload" {
			uploading[vol] = n
		}
	}
	rows.Close()

	// 再按先来后到挑一条能跑的
	cands, err := q.db.QueryContext(ctx,
		`SELECT id, volume, kind, name, payload, attempt FROM jobs WHERE state = ? ORDER BY id LIMIT 64`,
		stateQueued)
	if err != nil {
		return Job{}, false, err
	}
	var job Job
	found := false
	for cands.Next() {
		var c Job
		if err := cands.Scan(&c.ID, &c.Volume, &c.Kind, &c.Name, &c.Payload, &c.Attempt); err != nil {
			cands.Close()
			return Job{}, false, err
		}
		if found {
			continue
		}
		busy := running[c.Volume]
		if c.Kind == "upload" {
			// 同卷有非上传任务在跑时不插队；上传数量没到上限就让位
			if busy > uploading[c.Volume] || uploading[c.Volume] >= uploadLane {
				continue
			}
		} else if busy > 0 {
			continue
		}
		job, found = c, true
	}
	cands.Close()
	if !found {
		return Job{}, false, nil
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

// maxConnRetries 是传输层被掐断后的最大自动重试次数。
//
// 输入：无（测试可临时改小）
// 输出：次数上限
// 预期：引擎长期不可用时到点判失败并保留断点，让人看到原因并手动重试，
//
//	不无限打日志；断网恢复在 10 次退避（约 5 分钟）内可自愈
var maxConnRetries = 10

// run 执行任务并按需重试。
func (q *Queue) run(ctx context.Context, job Job) {
	q.mu.RLock()
	handler := q.handler
	q.mu.RUnlock()
	if handler == nil {
		q.finish(job.ID, errors.New(i18n.T("err.queue_no_handler")))
		return
	}
	connRetries := 0
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
		retryable := Retryable(err)
		if !retryable {
			// 传输层被上游掐断（引擎判死连接、网络中断）时任务 ctx 仍存活，
			// 这时可以安全重试：上传断点会跳过已完成的分片
			if !isConnInterrupted(err) {
				// 频道侧错误在 telegramstore 内部已包过一次提示，这里不再重复包
				if telegramstore.IsChannelUnavailable(err) {
					q.finish(job.ID, err)
				} else {
					q.finish(job.ID, telegramstore.DescribeError(err))
				}
				return
			}
			connRetries++
			if connRetries > maxConnRetries {
				q.finish(job.ID, fmt.Errorf("%s: %w", i18n.T("err.queue_conn_lost"), err))
				return
			}
		}
		wait := backoff(job.Attempt)
		if !retryable {
			// 连接中断单独计数退避，不跟限流等待混在一起
			wait = backoff(connRetries)
		}
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

// isConnInterrupted 判断错误是否来自传输层被上游打断。
//
// 输入：handler 返回的 error
// 输出：canceled/deadline/EOF/连接重置 之一时为 true
// 预期：只在任务 ctx 仍存活时调用（run 已先排除主动取消），
//
//	这类错误重试是安全的；其它确定性错误不走这里
func isConnInterrupted(err error) bool {
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return true
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return true
	case errors.Is(err, syscall.ECONNRESET), errors.Is(err, syscall.EPIPE):
		return true
	}
	return false
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
	// 进度只属于执行期，任务结束后不再保留
	q.progMu.Lock()
	delete(q.progress, id)
	q.progMu.Unlock()
	// 车道让出来后叫醒睡着的 worker：同卷上传并行后，结束的那条可能正是别人在等的名额
	q.signal()
	// 唤醒所有等待该任务结束的调用方
	q.notifyDoneClose(id)
}

// permanentError 标记确定性失败：重放同样的参数也不会成功。
type permanentError struct{ err error }

func (e permanentError) Error() string { return e.err.Error() }
func (e permanentError) Unwrap() error { return e.err }

// Permanent 把确定性错误标记成不重试。
//
// 输入：err 具体原因
// 输出：带标记的错误，Error/Unwrap 原样保留原因
// 预期：用于「目标已不存在」这类重放无意义的失败，
// 队列直接落 failed 并把原因显示给用户，不再无限重试
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return permanentError{err: err}
}

// Retryable 判断错误是否值得重试。
//
// 输入：任意 error
// 输出：true 表示可能是瞬时问题（断网、429、5xx）
// 预期：权限/频道/超限/断点失效/确定性失败重试只会浪费时间
func Retryable(err error) bool {
	if err == nil {
		return false
	}
	if _, ok := errors.AsType[permanentError](err); ok {
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
