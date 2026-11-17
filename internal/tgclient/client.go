package tgclient

// 统一创建 Telegram 客户端，内置 floodwait 自动退避。
//
// 默认参数开箱即用，不需要用户配置；Options 留口子给将来接配置文件。

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sync/atomic"
	"time"

	"github.com/gotd/contrib/middleware/floodwait"
	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/dcs"
	"github.com/gotd/td/tg"
	"golang.org/x/net/proxy"

	"mytgfs/internal/i18n"
)

// defaultProxy 是进程级默认代理地址（host:port），空表示不启用。
//
// 输入：由装配根在读到配置后调用 SetDefaultProxy 设置
// 输出：无
// 预期：所有 tgclient.New 的调用点不必各自传代理；显式 Options.Proxy 优先
var defaultProxy atomic.Value

// SetDefaultProxy 设置进程级默认 SOCKS5 代理。
//
// 输入：addr 形如 127.0.0.1:7890；空串表示关闭
// 输出：无
// 预期：只需在入口调用一次，后续所有客户端连接都经该代理
func SetDefaultProxy(addr string) { defaultProxy.Store(addr) }

// resolverFor 按代理地址构造显式走 SOCKS5 的解析器。
//
// 输入：addr 代理地址；空返回 nil 解析器
// 输出：dcs.Resolver 或 error
// 预期：MTProto 的 TCP 由 SOCKS5 建立在代理侧，与网页版同一出口；
//
//	不设置时 gotd 用默认解析器直接拨 DC IP
func resolverFor(addr string) (dcs.Resolver, error) {
	if addr == "" {
		return nil, nil
	}
	d, err := proxy.SOCKS5("tcp", addr, nil, proxy.Direct)
	if err != nil {
		return nil, err
	}
	cd, ok := d.(proxy.ContextDialer)
	if !ok {
		return nil, fmt.Errorf(i18n.T("err.proxy_dialer"), addr)
	}
	return dcs.Plain(dcs.PlainOptions{
		Dial: func(ctx context.Context, network, target string) (net.Conn, error) {
			return cd.DialContext(ctx, network, target)
		},
	}), nil
}

// ConnState 是主连接状态，屏蔽 gotd 的具体枚举。
type ConnState int

const (
	// ConnConnecting 正在建立连接。
	ConnConnecting ConnState = iota
	// ConnReady 连接已就绪，可以收发请求。
	ConnReady
	// ConnDisconnected 连接已断开，客户端会自动重连。
	ConnDisconnected
)

// Options 是可选的限速参数，零值表示使用默认值。
type Options struct {
	AdaptiveMin time.Duration // 自适应最小间隔，默认 500ms
	AdaptiveMax time.Duration // 自适应最大间隔，默认 5s
	MaxRetries  int           // 单个请求最大重试，默认 5
	MaxWait     time.Duration // 单次最大等待，默认 2 分钟
	// OnConnState 在主连接状态变化时回调。
	// 输入：连接状态；输出：无；预期：回调要短，不能阻塞连接生命周期。
	OnConnState func(ConnState)
	// OnAuthLost 在会话失效时回调（会话被吊销、退出登录、账号停用等）。
	// 输入：ctx 与导致判定的错误；输出：非 nil 时停止 Run
	// 预期：nil 时默认停止 Run，避免拿着失效会话无限重连空转
	OnAuthLost func(ctx context.Context, err error) error
	// Proxy 是 SOCKS5 代理地址（host:port）；空则用进程默认值（telegram.proxy）。
	// 输入：127.0.0.1:7890 之类；输出：无
	// 预期：设置后 MTProto 的 TCP 由代理建立
	Proxy string
	// BotToken 非空时，Run 在发现会话未授权时自动用它登录一次。
	// 输入：bot token 明文；输出：无
	// 预期：只给「首次运行 / 会话缺失」的场景用；留空则完全不做登录 RPC
	BotToken string
}

// Client 包装 telegram.Client 与 floodwait 调度器。
type Client struct {
	*telegram.Client
	waiter   *floodwait.Waiter
	botToken string
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

	// 默认在授权失效时停止 Run：失效会话无法自愈，继续重连只会空转
	onAuthLost := opts.OnAuthLost
	if onAuthLost == nil {
		onAuthLost = func(_ context.Context, err error) error { return err }
	}

	// 代理地址：显式 Options.Proxy 优先，其次进程默认值（由装配根从配置注入）
	addr := opts.Proxy
	if addr == "" {
		addr, _ = defaultProxy.Load().(string)
	}
	resolver, rerr := resolverFor(addr)
	if rerr != nil {
		slog.Warn(i18n.T("log.proxy_unavailable", rerr), "err", rerr)
	}

	client := telegram.NewClient(apiID, apiHash, telegram.Options{
		SessionStorage:    sess,
		Middlewares:       []telegram.Middleware{waiter},
		OnConnectionState: mapConnState(opts.OnConnState),
		OnAuthLost:        onAuthLost,
		// AllowCDN 让大文件走 CDN 边缘节点，而不是全部挤在主 DC
		AllowCDN: true,
		// Resolver 显式走 SOCKS5 时由代理建立 MTProto 的 TCP
		Resolver: resolver,
	})
	return &Client{Client: client, waiter: waiter, botToken: opts.BotToken}
}

// mapConnState 把 gotd 的连接状态映射成本包的精简枚举。
//
// 输入：回调（可为 nil）
// 输出：gotd 需要的回调；nil 时返回 nil
// 预期：Disconnected 包含所有非就绪/非连接中的状态，避免漏掉新枚举值
func mapConnState(fn func(ConnState)) func(telegram.ConnectionState) {
	if fn == nil {
		return nil
	}
	return func(s telegram.ConnectionState) {
		switch s {
		case telegram.ConnectionStateReady:
			fn(ConnReady)
		case telegram.ConnectionStateConnecting:
			fn(ConnConnecting)
		default:
			fn(ConnDisconnected)
		}
	}
}

// Run 启动 client，并让 floodwait 调度器包住整个生命周期。
//
// 输入：ctx；f 业务回调
// 输出：error
// 预期：ctx 取消时一起退出；flood 等待期间不会阻塞调度器
func (c *Client) Run(ctx context.Context, f func(context.Context) error) error {
	return c.waiter.Run(ctx, func(ctx context.Context) error {
		return c.Client.Run(ctx, func(ctx context.Context) error {
			if err := c.ensureLogin(ctx); err != nil {
				return err
			}
			return f(ctx)
		})
	})
}

// ensureLogin 在会话未授权时用 bot token 登录一次。
//
// 输入：ctx
// 输出：error；未配置 token、已授权或登录成功都返回 nil
// 预期：
//   - 未配置 token 时不做任何 RPC，保持原行为（未授权仍由调用方自己报错）
//   - 已有授权会话（user 或 bot）时不重新登录，避免把 user 会话顶掉
//
// 没有这一步时，只有 token 文件、没有会话文件的机器（全新安装、手机）
// 拿到的 auth key 从未在服务端登记，任何 RPC 都返回 401 AUTH_KEY_UNREGISTERED。
func (c *Client) ensureLogin(ctx context.Context) error {
	if c.botToken == "" {
		return nil
	}
	st, err := c.Auth().Status(ctx)
	if err != nil {
		return err
	}
	if st.Authorized {
		return nil
	}
	if _, err := c.Auth().Bot(ctx, c.botToken); err != nil {
		return fmt.Errorf("%s: %w", i18n.T("err.bot_login_prefix"), err)
	}
	return nil
}

// PooledAPI 返回一个把请求分散到最多 n 条连接的 API 客户端。
//
// 输入：n 连接数；<=1 时退回单连接
// 输出：API 客户端；关闭函数；error
// 预期：
//   - Telegram 单条 MTProto 连接的吞吐受线路往返限制，多条连接才能吃满带宽
//   - 关闭函数必须在 API 不再使用后调用，否则连接泄漏
func (c *Client) PooledAPI(n int) (*tg.Client, func(), error) {
	if n <= 1 {
		return c.API(), func() {}, nil
	}
	invoker, err := c.Client.Pool(int64(n))
	if err != nil {
		return nil, nil, fmt.Errorf(i18n.T("err.pool_create")+": %w", err)
	}
	return tg.NewClient(invoker), func() { _ = invoker.Close() }, nil
}
