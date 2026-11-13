package fsck

// 索引对账：把本地索引与频道全量节点逐条比对。
//
// 索引是可丢弃的缓存；Telegram 侧可能被手工改动（删消息、改名字、改父节点），
// 自动同步覆盖不到的漂移需要一次显式核对，并可选择按远端重建。

import (
	"context"
	"log/slog"

	"mytgfs/internal/i18n"
	"mytgfs/internal/index"
	tgsync "mytgfs/internal/sync"
	"mytgfs/internal/telegramstore"
)

// 问题类型。
const (
	// KindMissing 表示索引里的节点在远端已不存在。
	KindMissing = "missing"
	// KindMoved 表示同一节点在远端的路径与索引不同。
	KindMoved = "moved"
	// KindAdded 表示远端有、索引里没有的节点。
	KindAdded = "added"
)

// Problem 是一条漂移记录。
type Problem struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	MsgID  int    `json:"message_id"`
	Actual string `json:"actual,omitempty"`
}

// Report 是对账结果。
type Report struct {
	Checked  int
	Missing  int
	Moved    int
	Added    int
	Fixed    int
	Problems []Problem
}

// Source 提供远端全量节点。
type Source interface {
	AllNodes(ctx context.Context) ([]telegramstore.NodeInfo, error)
}

// Run 以远端全量节点为准核对本地索引。
//
// 输入：ctx；idx 本地索引；src 远端节点源；fix 是否按远端重建
// 输出：统计与问题列表；错误原样返回
// 预期：
//   - 本地有、远端无 => KindMissing
//   - 两边都有但路径不同 => KindMoved（Actual 为远端路径）
//   - 远端有、本地无 => KindAdded
//   - fix 时清空节点表并按远端整棵树重建
func Run(ctx context.Context, idx *index.Index, src Source, fix bool) (Report, error) {
	var rep Report
	nodes, err := src.AllNodes(ctx)
	if err != nil {
		return rep, err
	}
	remote, err := tgsync.Layout(nodes)
	if err != nil {
		return rep, err
	}
	local, err := idx.AllNodes(ctx)
	if err != nil {
		return rep, err
	}
	rep.Checked = len(local)

	seen := make(map[int]bool, len(local))
	for _, n := range local {
		seen[n.ID] = true
		r, ok := remote[n.ID]
		if !ok {
			rep.Missing++
			rep.Problems = append(rep.Problems, Problem{Path: n.Path, Kind: KindMissing, MsgID: n.ID})
			continue
		}
		if r.Path != n.Path {
			rep.Moved++
			rep.Problems = append(rep.Problems, Problem{Path: n.Path, Kind: KindMoved, MsgID: n.ID, Actual: r.Path})
		}
	}
	for id, r := range remote {
		if seen[id] {
			continue
		}
		rep.Added++
		rep.Problems = append(rep.Problems, Problem{Path: r.Path, Kind: KindAdded, MsgID: id})
	}

	if !fix || len(rep.Problems) == 0 {
		return rep, nil
	}
	n, err := tgsync.Rebuild(ctx, idx, nodes)
	if err != nil {
		return rep, err
	}
	slog.Debug(i18n.T("log.fsck_rebuilt"), "nodes", n)
	rep.Fixed = len(rep.Problems)
	return rep, nil
}
