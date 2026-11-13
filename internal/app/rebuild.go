package app

// 索引自动重建：本地 SQLite 只是缓存，丢了（换机、删库、升级）时
// 用 user 会话把频道历史重新翻一遍，不需要人工敲 sync --backfill。

import (
	"cmp"
	"context"
	"log/slog"
	"os"

	"mytgfs/internal/channel"
	"mytgfs/internal/config"
	"mytgfs/internal/crypto"
	"mytgfs/internal/i18n"
	"mytgfs/internal/index"
	"mytgfs/internal/sessionstore"
	tgsync "mytgfs/internal/sync"
	"mytgfs/internal/tgclient"
)

// rebuildPageSize 是重建时每页拉取的原始消息条数。
const rebuildPageSize = 100

// userSessionPath 返回 user 会话文件路径，Options 可覆盖。
//
// 输入：无
// 输出：user.session 路径
// 预期：默认与 CLI 约定一致，测试可指向临时目录
func (a *App) userSessionPath() string {
	return cmp.Or(a.opts.UserSessionPath, DefaultUserSessionPath)
}

// New 用已加载的配置与装配参数构造 App，避免调用方重复读盘。
func New(cfg config.Config, opts Options) *App { return &App{cfg: cfg, opts: opts} }

// RebuildIndexIfEmpty 在索引为空时用 user 会话重建。
//
// 输入：ctx；idx 索引；channelID 频道 ID
// 输出：error
// 预期：
//   - 索引里已有文件：直接返回，不做任何网络请求
//   - 没有 user 会话：跳过并返回 nil（只剩 bot 会话时 bot 倒不了历史）
//   - 重建成功：索引里恢复路径、大小、摘要
func (a *App) RebuildIndexIfEmpty(ctx context.Context, idx *index.Index, channelID int64) error {
	n, err := idx.CountFiles(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	if _, serr := os.Stat(a.userSessionPath()); serr != nil {
		slog.Info(i18n.T("log.rebuild_skipped"))
		return nil
	}
	key, err := crypto.LoadOrCreateKey(cmp.Or(a.opts.KeyPath, DefaultKeyPath))
	if err != nil {
		return err
	}
	client := tgclient.New(a.cfg.Telegram.APIID, a.cfg.APIHash(),
		&sessionstore.Encrypted{Path: a.userSessionPath(), Key: key},
		tgclient.Options{})
	var total int
	err = client.Run(ctx, func(ctx context.Context) error {
		// messages.getHistory 需要真实 access_hash，先按频道 ID 解析
		res, rerr := channel.Resolve(ctx, client.API(), channelID)
		if rerr != nil {
			return rerr
		}
		f := tgsync.TelegramFetcher{API: client.API(), ChannelID: channelID, AccessHash: res.AccessHash}
		total, rerr = tgsync.Rebuild(ctx, idx, f, rebuildPageSize, 0)
		return rerr
	})
	if err != nil {
		return err
	}
	slog.Info(i18n.T("log.rebuild_done"), "channel", channelID, "messages", total)
	return nil
}
