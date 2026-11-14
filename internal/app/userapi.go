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
	apiHash := a.cfg.APIHash()
	if apiHash == "" {
		return ErrMissingAPIHash
	}
	key, err := crypto.LoadOrCreateKey(cmp.Or(a.opts.KeyPath, DefaultKeyPath))
	if err != nil {
		return fmt.Errorf("%s: %w", i18n.T("err.session_key_prefix"), err)
	}
	client := tgclient.New(a.cfg.Telegram.APIID, apiHash,
		&sessionstore.Encrypted{Path: userSession, Key: key},
		tgclient.Options{})

	var fnErr error
	runErr := client.Run(ctx, func(ctx context.Context) error {
		fnErr = fn(ctx, client.API())
		// 返回 nil 让 gotd 收尾退出，避免把业务错误包成连接错误
		return nil
	})
	if fnErr != nil {
		return fnErr
	}
	return runErr
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
