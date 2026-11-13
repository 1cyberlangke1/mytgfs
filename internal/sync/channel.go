package sync

// 频道增量同步：走第三方客户端同款机制 updates.getChannelDifference。
//
// Telegram 的频道有独立的 pts（persistent timestamp）游标：
// 本地记住 pts，下次只请求 pts 之后的差异，服务端返回新消息与删除等事件。
// 这条通道 bot 账号也能用（文档：Both users and bots），因此常规同步不再
// 依赖 user 会话；user 会话只用于首次深挖历史（messages.getHistory 是 user-only）。

import (
	"context"
	"errors"
	"fmt"

	"github.com/gotd/td/tgerr"

	"mytgfs/internal/i18n"
	"mytgfs/internal/index"
)

// MetaChannelPts 是索引里保存频道 pts 游标的键名。
const MetaChannelPts = index.MetaKeyChannelPts

// bootstrapPts 是首次同步的起点。
//
// Telegram 不接受 pts=0（PERSISTENT_TIMESTAMP_EMPTY），而 pts=1 表示
// 「从这个频道的开头开始算差异」；实测会返回自身份以来全部消息。
const bootstrapPts = 1

// maxDiffRounds 限制单次同步的差异轮数，避免服务端异常时死循环。
const maxDiffRounds = 200

// Difference 是一次频道差异的结果。
type Difference struct {
	Pts      int       // 下一轮应使用的 pts
	Final    bool      // true 表示已追平，可停止
	TooLong  bool      // true 表示差异过大，只剩最新消息（需另行回填历史）
	Messages []Message // 新消息 / 被编辑的消息
	Deleted  []int     // 被删除的消息 ID
}

// ChannelDiffer 拉取频道差异，便于单元测试替身。
type ChannelDiffer interface {
	ChannelDifference(ctx context.Context, pts int, limit int) (Difference, error)
}

// ChannelSyncResult 汇总一次增量同步的效果。
type ChannelSyncResult struct {
	Pulled    int      // 实际写入索引的消息条数
	Deleted   int      // 从索引删除的条数
	Pts       int      // 同步后的 pts
	Truncated bool     // 是否发生差异过大（历史可能有缺口）
	Rounds    int      // 差异调用轮数
	Changed   []string // 新增/编辑的路径，供挂载点刷新
	Removed   []string // 删除的路径，供挂载点刷新
}

// IsPtsInvalid 判断错误是否为本地 pts 失效。
//
// 输入：任意 error
// 输出：true 表示把游标重置到 bootstrapPts 后重试
// 预期：PERSISTENT_TIMESTAMP_EMPTY / PERSISTENT_TIMESTAMP_INVALID 视为失效；
//
//	PERSISTENT_TIMESTAMP_OUTDATED 属于服务端复制延迟，交由上层重试，不重置
func IsPtsInvalid(err error) bool {
	if err == nil {
		return false
	}
	e, ok := tgerr.As(err)
	if !ok {
		return false
	}
	return e.Type == "PERSISTENT_TIMESTAMP_EMPTY" || e.Type == "PERSISTENT_TIMESTAMP_INVALID"
}

// SyncChannel 用 pts 游标增量同步频道。
//
// 输入：ctx；idx 索引；d 差异源；limit 单轮最多事件数
// 输出：同步统计；error
// 预期：每轮成功都持久化 pts；中途失败时索引只保留已完整应用的那一轮；
//
//	pts 失效自动从 bootstrapPts 重来一次；差异过大时标记 Truncated
func SyncChannel(ctx context.Context, idx *index.Index, d ChannelDiffer, limit int) (ChannelSyncResult, error) {
	var res ChannelSyncResult
	if limit <= 0 {
		limit = 100
	}

	pts := bootstrapPts
	v, ok, err := idx.GetMeta(ctx, MetaChannelPts)
	if err != nil {
		return res, err
	}
	if ok && v > 0 {
		pts = int(v)
	}
	res.Pts = pts

	retried := false
	for range maxDiffRounds {
		diff, err := d.ChannelDifference(ctx, pts, limit)
		if err != nil {
			// 本地 pts 与服务端对不上：重置一次再试
			if IsPtsInvalid(err) && !retried {
				retried = true
				pts = bootstrapPts
				if err := idx.PutMeta(ctx, MetaChannelPts, int64(pts)); err != nil {
					return res, err
				}
				continue
			}
			return res, err
		}
		res.Rounds++

		paths, err := Replay(ctx, idx, diff.Messages)
		if err != nil {
			return res, err
		}
		res.Pulled += len(paths)
		res.Changed = append(res.Changed, paths...)
		for _, id := range diff.Deleted {
			// 先取路径再删，才能通知挂载点移除对应节点
			if p, ok, perr := idx.PathByMessageID(ctx, id); perr != nil {
				return res, perr
			} else if ok {
				res.Removed = append(res.Removed, p)
			}
			if err := idx.DeleteByMessageID(ctx, id); err != nil {
				return res, err
			}
			res.Deleted++
		}

		if diff.TooLong {
			res.Truncated = true
		}
		if diff.Pts > 0 {
			pts = diff.Pts
		}
		// 先落盘 pts 再继续：崩溃时最多重放一轮，不会丢事件
		if err := idx.PutMeta(ctx, MetaChannelPts, int64(pts)); err != nil {
			return res, err
		}
		res.Pts = pts

		if diff.Final || diff.TooLong {
			return res, nil
		}
	}
	return res, fmt.Errorf(i18n.T("err.diff_rounds_exceeded"), maxDiffRounds, pts)
}

// ErrNoDiffer 表示调用方没有提供差异源。
var ErrNoDiffer = errors.New(i18n.T("err.no_differ"))
