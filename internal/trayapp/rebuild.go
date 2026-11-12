package trayapp

// 索引自动重建：本地 SQLite 只是缓存，丢了（换机、删库、升级）时
// 用 user 会话把频道历史重新翻一遍，不需要人工敲 sync --backfill。

import (
	"context"
	"log/slog"
	"os"

	"mytgfs/internal/channel"
	"mytgfs/internal/config"
	"mytgfs/internal/crypto"
	"mytgfs/internal/index"
	"mytgfs/internal/sessionstore"
	tgsync "mytgfs/internal/sync"
	"mytgfs/internal/tgclient"
)

// userSessionPath 是用户会话文件，messages.getHistory 只有 user 可用。
const userSessionPath = "secrets/user.session"

// rebuildPageSize 是重建时每页拉取的原始消息条数。
const rebuildPageSize = 100

// RebuildIndexIfEmpty 在索引为空时用 user 会话重建。
//
// 输入：ctx；cfg 配置；idx 索引；channelID 频道 ID
// 输出：error
// 预期：
//   - 索引里已有文件：直接返回，不做任何网络请求
//   - 没有 user 会话：跳过并返回 nil（只剩 bot 会话时 bot 倒不了历史）
//   - 重建成功：索引里恢复路径、大小、摘要
func RebuildIndexIfEmpty(ctx context.Context, cfg config.Config, idx *index.Index, channelID int64) error {
	n, err := idx.CountFiles(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	if _, serr := os.Stat(userSessionPath); serr != nil {
		slog.Info("索引为空，但没有 user 会话，跳过自动重建")
		return nil
	}
	key, err := crypto.LoadOrCreateKey("secrets/session.key")
	if err != nil {
		return err
	}
	client := tgclient.New(cfg.Telegram.APIID, cfg.APIHash(),
		&sessionstore.Encrypted{Path: userSessionPath, Key: key},
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
	slog.Info("索引已按频道历史重建", "channel", channelID, "messages", total)
	return nil
}
