package app

// 索引自动重建：本地 SQLite 只是缓存，丢了（换机、删库、升级）时
// 用 user 会话全量拉一次频道历史，按每条消息自带的名字与父消息号拼出整棵树。
//
// 不需要根消息、不需要置顶：父消息号缺失或成环的节点直接挂到卷根。

import (
	"cmp"
	"context"
	"log/slog"
	"os"

	"mytgfs/internal/channel"
	"mytgfs/internal/config"
	"mytgfs/internal/i18n"
	"mytgfs/internal/index"
	tgsync "mytgfs/internal/sync"
	"mytgfs/internal/telegramstore"
	"mytgfs/internal/tgclient"
)

// userSessionPath 返回 user 会话文件路径，Options 可覆盖。
func (a *App) userSessionPath() string {
	return cmp.Or(a.opts.UserSessionPath, DefaultUserSessionPath)
}

// New 用已加载的配置与装配参数构造 App，避免调用方重复读盘。
func New(cfg config.Config, opts Options) *App { return &App{cfg: cfg, opts: opts} }

// AllNodes 用 user 会话全量拉频道历史，解析出所有节点。
//
// 输入：ctx；channelID 频道 ID
// 输出：远端节点列表；error
// 预期：没有 user 会话时返回空列表（messages.getHistory 是 user-only）
func (a *App) AllNodes(ctx context.Context, channelID int64) ([]telegramstore.NodeInfo, error) {
	if _, err := os.Stat(a.userSessionPath()); err != nil {
		return nil, nil
	}
	var nodes []telegramstore.NodeInfo
	err := a.runClient(ctx, a.userSessionPath(), tgclient.Options{}, func(ctx context.Context, c *tgclient.Client) error {
		ch, rerr := channel.Resolve(ctx, c.API(), channelID)
		if rerr != nil {
			return rerr
		}
		h := &tgsync.TelegramHistory{API: c.API(), ChannelID: ch.ChannelID}
		var herr error
		nodes, herr = h.AllNodes(ctx)
		return herr
	})
	if err != nil {
		return nil, err
	}
	return nodes, nil
}

// adoptChannel 确认本地索引属于这个频道。
//
// 输入：ctx；idx 本地索引；channelID 配置里的频道 id（-100... 形式）
// 输出：error
// 预期：
//   - 首次使用：只记下频道 id，不动任何节点
//   - 频道换了（手改 chat_id、同名挂到别的频道）：清空节点、已上传待索引账本与
//     pts 游标，让上层按新频道重建；否则面板会显示上一个频道的文件，
//     读取还会拿着别的频道的消息号
func (a *App) adoptChannel(ctx context.Context, idx *index.Index, channelID int64) error {
	prev, ok, err := idx.GetMeta(ctx, index.MetaKeyChannelID)
	if err != nil {
		return err
	}
	if ok && prev != channelID {
		slog.Warn(i18n.T("log.channel_changed"), "old", prev, "new", channelID)
		if err := idx.ClearNodes(ctx); err != nil {
			return err
		}
		if err := idx.ClearPendingDone(ctx); err != nil {
			return err
		}
		// pts 是频道专属游标：换频道必须归零，下次同步从 bootstrap 重新拉
		if err := idx.PutMeta(ctx, index.MetaKeyChannelPts, 0); err != nil {
			return err
		}
		// 旧频道的 access_hash 缓存一起清掉，避免换回去时用到过期值
		_ = idx.DeleteMeta(ctx, channelCacheKey(prev))
	}
	return idx.PutMeta(ctx, index.MetaKeyChannelID, channelID)
}

// RebuildIndexIfEmpty 在索引为空时按远端全量节点重建。
//
// 输入：ctx；idx 索引；channelID 该卷频道 ID
// 输出：error
// 预期：
//   - 索引里已有文件：直接返回，不做任何网络请求
//   - 没有 user 会话：跳过并返回 nil
//   - 历史里没有节点消息：视为空卷，不写任何节点
func (a *App) RebuildIndexIfEmpty(ctx context.Context, idx *index.Index, channelID int64) error {
	n, err := idx.CountNodes(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	if _, serr := os.Stat(a.userSessionPath()); serr != nil {
		slog.Info(i18n.T("log.rebuild_no_user"))
		return nil
	}
	count, err := a.RebuildIndex(ctx, idx, channelID)
	if err != nil {
		return err
	}
	if count == 0 {
		slog.Info(i18n.T("log.rebuild_empty"), "channel", channelID)
		return nil
	}
	slog.Info(i18n.T("log.rebuild_done"), "channel", channelID)
	return nil
}

// RebuildIndex 无条件按远端全量节点重建本地索引。
//
// 输入：ctx；idx 索引；channelID 该卷频道 ID
// 输出：写入的节点数
// 预期：远端为空时返回 0 且不改本地；没有 user 会话时返回错误，不静默成功
func (a *App) RebuildIndex(ctx context.Context, idx *index.Index, channelID int64) (int, error) {
	nodes, err := a.AllNodes(ctx, channelID)
	if err != nil {
		return 0, err
	}
	if len(nodes) == 0 {
		// AllNodes 在无 user 会话时只返回空列表：这里补判一次，
		// 避免把「连不上/没会话」当成空频道而不报错
		if _, serr := os.Stat(a.userSessionPath()); serr != nil {
			return 0, ErrUserSessionMissing
		}
		return 0, nil
	}
	return tgsync.Rebuild(ctx, idx, nodes)
}
