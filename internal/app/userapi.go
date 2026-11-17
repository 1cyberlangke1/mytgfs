package app

// 用 user 会话执行频道级写操作。
//
// 头像这类频道设置需要 canChangeInfo 权限，bot 管理员不一定被授予；
// user 会话是频道创建者/管理员，写入权限更可靠。连接用完即退。

import (
	"cmp"
	"context"
	"fmt"
	"os"

	"github.com/gotd/td/tg"

	"mytgfs/internal/channel"
	"mytgfs/internal/crypto"
	"mytgfs/internal/i18n"
	"mytgfs/internal/sessionstore"
	"mytgfs/internal/tgclient"
)

// runClient 用指定会话开一次临时连接执行 fn。
//
// 输入：ctx；path 会话文件；opts 连接选项；fn 连接就绪后的回调（拿到客户端）
// 输出：fn 的业务错误优先返回，其次才是连接错误
// 预期：一次性操作使用，fn 返回后连接立即关闭，不常驻
func (a *App) runClient(ctx context.Context, path string, opts tgclient.Options, fn func(ctx context.Context, c *tgclient.Client) error) error {
	apiHash := a.cfg.APIHash()
	if apiHash == "" {
		return ErrMissingAPIHash
	}
	key, err := crypto.LoadOrCreateKey(cmp.Or(a.opts.KeyPath, DefaultKeyPath))
	if err != nil {
		return fmt.Errorf("%s: %w", i18n.T("err.session_key_prefix"), err)
	}
	client := tgclient.New(a.cfg.Telegram.APIID, apiHash,
		&sessionstore.Encrypted{Path: path, Key: key}, opts)

	var fnErr error
	runErr := client.Run(ctx, func(ctx context.Context) error {
		fnErr = fn(ctx, client)
		// 返回 nil 让 gotd 收尾退出，避免把业务错误包成连接错误
		return nil
	})
	if fnErr != nil {
		return fnErr
	}
	return runErr
}

// withUserAPI 用 user 会话开一次临时连接执行 fn。
//
// 输入：ctx；fn 在连接就绪后的回调
// 输出：fn 的错误优先返回；否则返回连接错误
// 预期：会话文件或 api_hash 缺失时直接返回对应哨兵错误，不新建会话
func (a *App) withUserAPI(ctx context.Context, fn func(ctx context.Context, api *tg.Client) error) error {
	userSession := a.userSessionPath()
	if _, err := os.Stat(userSession); err != nil {
		return ErrUserSessionMissing
	}
	return a.runClient(ctx, userSession, tgclient.Options{}, func(ctx context.Context, c *tgclient.Client) error {
		return fn(ctx, c.API())
	})
}

// withBotAPI 用 bot 会话开一次临时连接执行 fn。
//
// 输入：ctx；fn 在连接就绪后拿到已登录的客户端
// 输出：fn 的错误优先返回；否则返回连接错误
// 预期：建卷读用户名、挂载已有频道这类一次性操作使用，不常驻连接
func (a *App) withBotAPI(ctx context.Context, fn func(ctx context.Context, c *tgclient.Client) error) error {
	return a.runClient(ctx, cmp.Or(a.opts.SessionPath, DefaultSessionPath),
		tgclient.Options{BotToken: a.cfg.BotToken()}, fn)
}

// SetChannelPhoto 用 user 会话把 JPEG 写成频道头像。
//
// 输入：ctx；channelID 频道 id；accessHash 为 0 时自动解析；jpeg 512×512 JPEG
// 输出：error
// 预期：写成功后 Telegram 侧头像立即变化；失败原样透出，不吞错误
func (a *App) SetChannelPhoto(ctx context.Context, channelID, accessHash int64, jpeg []byte) error {
	return a.withUserAPI(ctx, func(ctx context.Context, api *tg.Client) error {
		return channel.SetPhoto(ctx, api, channelID, accessHash, jpeg)
	})
}

// GrantChannelBot 用 user 会话把 bot 设为频道管理员。
//
// 输入：channelID 频道号；accessHash 为 0 时现解析；botUsername 不含 @
// 输出：error
// 预期：覆盖式设置 BotRights（发消息/编辑/改频道信息）；老卷缺 ChangeInfo 时调用
func (a *App) GrantChannelBot(ctx context.Context, channelID, accessHash int64, botUsername string) error {
	return a.withUserAPI(ctx, func(ctx context.Context, api *tg.Client) error {
		return channel.GrantBotByUsername(ctx, api, channelID, accessHash, botUsername)
	})
}
