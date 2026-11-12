package tgclient

// 统一创建 Telegram 客户端，内置 floodwait 自动退避。
//
// 默认参数开箱即用，不需要用户配置；Options 留口子给将来接配置文件。

import (
	"context"
	"time"

	"github.com/gotd/contrib/middleware/floodwait"
	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
)

// Options 是可选的限速参数，零值表示使用默认值。
type Options struct {
	AdaptiveMin time.Duration // 自适应最小间隔，默认 500ms
	AdaptiveMax time.Duration // 自适应最大间隔，默认 5s
	MaxRetries  int           // 单个请求最大重试，默认 5
	MaxWait     time.Duration // 单次最大等待，默认 2 分钟
}

// Client 包装 telegram.Client 与 floodwait 调度器。
type Client struct {
	*telegram.Client
	waiter *floodwait.Waiter
}

// New 创建带 floodwait 中间件的客户端。
//
// 输入：apiID/apiHash；session 会话存储；opts 可选限速参数（零值用默认）
// 输出：*Client
// 预期：收到 FLOOD_WAIT 时自动等待重试，不会把等待错误抛给调用方
func New(apiID int, apiHash string, sess session.Storage, opts Options) *Client {
	if opts.AdaptiveMin <= 0 {
		opts.AdaptiveMin = 500 * time.Millisecond
	}
	if opts.AdaptiveMax <= 0 {
		opts.AdaptiveMax = 5 * time.Second
	}
	if opts.MaxRetries <= 0 {
		opts.MaxRetries = 5
	}
	if opts.MaxWait <= 0 {
		opts.MaxWait = 2 * time.Minute
	}

	waiter := floodwait.NewWaiter().
		WithAdaptivePacing(opts.AdaptiveMin, opts.AdaptiveMax).
		WithMaxRetries(opts.MaxRetries).
		WithMaxWait(opts.MaxWait)

	client := telegram.NewClient(apiID, apiHash, telegram.Options{
		SessionStorage: sess,
		Middlewares:    []telegram.Middleware{waiter},
	})
	return &Client{Client: client, waiter: waiter}
}

// Run 启动 client，并让 floodwait 调度器包住整个生命周期。
//
// 输入：ctx；f 业务回调
// 输出：error
// 预期：ctx 取消时一起退出；flood 等待期间不会阻塞调度器
func (c *Client) Run(ctx context.Context, f func(context.Context) error) error {
	return c.waiter.Run(ctx, func(ctx context.Context) error {
		return c.Client.Run(ctx, f)
	})
}
