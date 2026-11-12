package queue

// 串行任务队列：把「发消息 + 上传」这类会被 Telegram 限流的操作排成一队。
//
// 用途：
//   - 批量上传时不让几十个文件同时打 Telegram，避免 FLOOD_WAIT
//   - 瞬时错误（网络抖动、临时 5xx）自动重试
//   - 面板能读到当前队列，知道哪些任务在跑
//
// 串行是刻意的：Telegram 每个频道每秒只能收一条消息，并发发只会换来限流。

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"mytgfs/internal/i18n"
	"mytgfs/internal/telegramstore"
)

// state 是任务状态。
type state string

const (
	stateQueued  state = "queued"
	stateRunning state = "running"
	stateDone    state = "done"
	stateFailed  state = "failed"
)

// Item 是一个任务的快照。
type Item struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	State   string `json:"state"`
	Attempt int    `json:"attempt"`
	Err     string `json:"error,omitempty"`
}

// Queue 是串行任务队列。
type Queue struct {
	sem     chan struct{}
	retries int
	backoff time.Duration

	mu    sync.Mutex
	next  int64
	items map[int64]*item
	order []int64
}

// item 是内部任务记录。
type item struct {
	Item
}

// New 创建队列。
//
// 输入：workers 并发槽数（<=0 按 1）；retries 瞬时错误重试次数（<0 按 0）
// 输出：*Queue
// 预期：workers=1 时任务严格串行，符合 Telegram 单频道 1 msg/s 的限制
func New(workers, retries int) *Queue {
	if workers <= 0 {
		workers = 1
	}
	if retries < 0 {
		retries = 0
	}
	return &Queue{
		sem:     make(chan struct{}, workers),
		retries: retries,
		backoff: time.Second,
		items:   map[int64]*item{},
	}
}

// Do 提交任务并等待完成。
//
// 输入：ctx 取消；name 任务名（用于面板展示）；fn 实际动作
// 输出：fn 的最终错误
// 预期：
//   - 排队期间 ctx 取消则不入队
//   - 可重试错误按 1s、2s、4s 退避重试，最多 retries 次
//   - 频道失效、文件过大、消息已删这类错误立即失败，重试无意义
func (q *Queue) Do(ctx context.Context, name string, fn func(context.Context) error) error {
	select {
	case q.sem <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-q.sem }()

	id := q.add(name)
	it := q.get(id)
	q.setRunning(it)

	var err error
	for attempt := 0; attempt <= q.retries; attempt++ {
		if attempt > 0 {
			// 退避等待，期间可被取消
			wait := q.backoff << (attempt - 1)
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				err = ctx.Err()
				q.finish(it, err)
				return err
			}
		}
		q.setAttempt(it, attempt+1)
		err = fn(ctx)
		if err == nil {
			q.finish(it, nil)
			return nil
		}
		if ctx.Err() != nil || !Retryable(err) {
			break
		}
		slog.Debug(i18n.T("log.queue_retry", name, attempt+1, err), "task", name)
	}
	q.finish(it, err)
	return err
}

// Retryable 判断错误是否值得重试。
//
// 输入：任意 error
// 输出：true 表示可能是瞬时问题
// 预期：权限/频道/超限/断点失效这类是确定性错误，重试只会浪费时间
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

// add 登记一个排队任务。
func (q *Queue) add(name string) int64 {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.next++
	id := q.next
	q.items[id] = &item{Item: Item{ID: id, Name: name, State: string(stateQueued)}}
	q.order = append(q.order, id)
	// 只保留最近 100 条，避免长跑进程内存一直涨
	if len(q.order) > 100 {
		drop := q.order[:len(q.order)-100]
		for _, old := range drop {
			delete(q.items, old)
		}
		q.order = q.order[len(q.order)-100:]
	}
	return id
}

func (q *Queue) get(id int64) *item {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.items[id]
}

func (q *Queue) setRunning(it *item) {
	q.mu.Lock()
	defer q.mu.Unlock()
	it.State = string(stateRunning)
}

func (q *Queue) setAttempt(it *item, n int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	it.Attempt = n
}

func (q *Queue) finish(it *item, err error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if err != nil {
		it.State = string(stateFailed)
		it.Err = err.Error()
		return
	}
	it.State = string(stateDone)
}

// Snapshot 返回任务快照，按提交顺序。
//
// 输入：无
// 输出：任务列表副本
// 预期：供 HTTP 面板读取，不加锁暴露内部结构
func (q *Queue) Snapshot() []Item {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]Item, 0, len(q.order))
	for _, id := range q.order {
		if it, ok := q.items[id]; ok {
			out = append(out, it.Item)
		}
	}
	return out
}

// Pending 返回排队中或运行中的任务数。
func (q *Queue) Pending() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	n := 0
	for _, it := range q.items {
		if it.State == string(stateQueued) || it.State == string(stateRunning) {
			n++
		}
	}
	return n
}
