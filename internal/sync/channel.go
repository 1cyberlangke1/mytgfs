package sync

// 频道增量同步：走第三方客户端同款机制 updates.getChannelDifference。
//
// Telegram 的频道有独立的 pts（persistent timestamp）游标：本地记住 pts，
// 下次只请求 pts 之后的差异。差异里给的是消息号，本地按消息号直接取节点内容
// 并刷新索引；非本程序格式的消息（聊天消息、替身文本）忽略即可。

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
	Pts     int   // 下一轮应使用的 pts
	Final   bool  // true 表示已追平，可停止
	TooLong bool  // true 表示差异过大（历史可能有缺口，需上层全量重建）
	Changed []int // 新增或被编辑的消息 ID
	Deleted []int // 被删除的消息 ID
}

// ChannelDiffer 拉取频道差异，便于单元测试替身。
type ChannelDiffer interface {
	ChannelDifference(ctx context.Context, pts int, limit int) (Difference, error)
}

// ChannelSyncResult 汇总一次增量同步的效果。
type ChannelSyncResult struct {
	Pulled    int      // 应用成功的变更条数
	Deleted   int      // 从索引移除的节点数
	Pts       int      // 同步后的 pts
	Truncated bool     // 差异过大，建议上层全量重建
	Rounds    int      // 差异调用轮数
	Changed   []string // 新增/编辑的路径，供界面刷新
	Removed   []string // 删除的路径，供界面刷新
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
// 输入：ctx；idx 索引；d 差异源；st 节点读取；limit 单轮最多事件数
// 输出：同步统计；error
// 预期：
//   - 每轮成功都持久化 pts；崩溃最多重放一轮
//   - 变更消息按消息号取节点内容，新增/改名/元数据变化都能就地应用
//   - pts 失效自动从 bootstrapPts 重来一次；差异过大时仅标记 Truncated
func SyncChannel(ctx context.Context, idx *index.Index, d ChannelDiffer, st NodeSource, limit int) (ChannelSyncResult, error) {
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

		pulled, changed, removed, aerr := applyChanged(ctx, idx, st, diff.Changed)
		if aerr != nil {
			return res, aerr
		}
		res.Pulled += pulled
		res.Changed = append(res.Changed, changed...)
		res.Removed = append(res.Removed, removed...)

		for _, id := range diff.Deleted {
			rm, _, derr := DeleteRemote(ctx, idx, id)
			if derr != nil {
				return res, derr
			}
			res.Deleted += len(rm)
			res.Removed = append(res.Removed, rm...)
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

// applyChanged 应用一批变更事件；非节点消息直接忽略。
func applyChanged(ctx context.Context, idx *index.Index, st NodeSource, ids []int) (pulled int, changed, removed []string, err error) {
	for _, id := range ids {
		c, rm, handled, aerr := ApplyChange(ctx, idx, st, id)
		if aerr != nil {
			return pulled, changed, removed, aerr
		}
		if !handled {
			continue
		}
		pulled++
		changed = append(changed, c...)
		removed = append(removed, rm...)
	}
	return pulled, changed, removed, nil
}

// ErrNoDiffer 表示调用方没有提供差异源。
var ErrNoDiffer = errors.New(i18n.T("err.no_differ"))
