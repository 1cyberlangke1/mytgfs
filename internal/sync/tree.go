package sync

// 按远端父指针重建与刷新本地索引。
//
// 每条消息自述名字与父消息号：全量拉一次历史就能把整棵树拼起来，
// 不需要根消息，也不需要置顶。父消息缺失或成环的节点挂到卷根 "/"。

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"mytgfs/internal/i18n"
	"mytgfs/internal/index"
	"mytgfs/internal/telegramstore"
)

// NodeSource 是按消息号取节点内容的远端能力。
type NodeSource interface {
	FetchNodes(ctx context.Context, ids []int) (map[int]telegramstore.NodeInfo, error)
}

// maxRebuildNodes 是单次重建的节点上限，防止异常数据把内存打满。
const maxRebuildNodes = 500000

// maxParentHops 限制沿父链上溯的深度，防止成环时死循环。
const maxParentHops = 4096

// Layout 把全量节点列表摊成本地索引记录。
//
// 输入：nodes 远端全量节点（顺序无关）
// 输出：消息号 → 带派生路径的节点
// 预期：
//   - 父消息号指向不存在的节点或成环时，该节点挂到卷根
//   - 同名冲突按消息号从大到小保留一条，结果与输入顺序无关
func Layout(nodes []telegramstore.NodeInfo) (map[int]index.Node, error) {
	if len(nodes) > maxRebuildNodes {
		return nil, fmt.Errorf(i18n.T("err.rebuild_too_large"), maxRebuildNodes)
	}
	byID := make(map[int]telegramstore.NodeInfo, len(nodes))
	for _, n := range nodes {
		byID[n.MessageID] = n
	}
	ids := slices.Sorted(maps.Keys(byID))
	slices.Reverse(ids)

	// parentOf 给出「有效父消息号」：父不存在、自指、或落在环里时归零。
	// 先按每个节点上溯一次父链，把检测到的环上所有节点标成归零，
	// 这样写进本地索引的 parent_id 本身就不成环，而不是只靠遍历时 visited 兜底。
	parentOf := make(map[int]int, len(ids))
	cycle := make(map[int]bool, len(ids))
	for _, id := range ids {
		if _, done := parentOf[id]; done {
			continue
		}
		chain := make([]int, 0, 4)
		pos := map[int]int{}
		cur := id
		for cur > 0 {
			if _, computed := parentOf[cur]; computed {
				break
			}
			if start, seen := pos[cur]; seen {
				for _, c := range chain[start:] {
					cycle[c] = true
				}
				break
			}
			pos[cur] = len(chain)
			chain = append(chain, cur)
			n, ok := byID[cur]
			if !ok {
				break
			}
			cur = n.Parent
		}
		for _, c := range chain {
			pid := byID[c].Parent
			if pid <= 0 || pid == c || cycle[c] || cycle[pid] {
				pid = 0
			} else if _, ok := byID[pid]; !ok {
				pid = 0
			}
			parentOf[c] = pid
		}
	}

	paths := make(map[int]string, len(ids))
	var resolve func(id int, depth int) string
	resolve = func(id int, depth int) string {
		if p, ok := paths[id]; ok {
			return p
		}
		pid := parentOf[id]
		// 父链已被归零处理，但仍限深：异常数据下不死循环
		if depth > maxParentHops {
			pid = 0
		}
		var p string
		if pid == 0 {
			p = "/" + byID[id].Name
		} else {
			p = joinPath(resolve(pid, depth+1), byID[id].Name)
		}
		paths[id] = p
		return p
	}

	out := make(map[int]index.Node, len(ids))
	used := map[string]bool{}
	for _, id := range ids {
		n := byID[id]
		p := resolve(id, 0)
		if used[p] {
			continue
		}
		used[p] = true
		out[id] = index.Node{
			ID: id, ParentID: parentOf[id], Name: n.Name, IsDir: n.IsDir, Path: p,
			DocumentID: n.DocumentID, AccessHash: n.AccessHash,
			FileReference: n.FileReference, Size: n.Size, Mtime: n.Mtime,
			Sha256: n.Sha,
		}
	}
	return out, nil
}

// Rebuild 用全量节点重建本地索引。
//
// 输入：ctx；idx 本地索引；nodes 远端全量节点
// 输出：写入的文件节点数；error
// 预期：先清空本地节点表，远端是唯一权威
func Rebuild(ctx context.Context, idx *index.Index, nodes []telegramstore.NodeInfo) (int, error) {
	layout, err := Layout(nodes)
	if err != nil {
		return 0, err
	}
	if err := idx.ClearNodes(ctx); err != nil {
		return 0, err
	}
	files := 0
	for _, n := range layout {
		if err := idx.PutNode(ctx, n); err != nil {
			return files, err
		}
		if !n.IsDir {
			files++
		}
	}
	return files, nil
}

// RefreshNode 按远端消息刷新一个本地节点。
//
// 输入：ctx；idx；st；id 消息号
// 输出：节点路径、是否仍存在、error
// 预期：
//   - 远端消息已删：连同子树从本地删掉，返回 ok=false
//   - 名字或父消息号变化：走 MoveNode 刷新整棵子树路径
//   - 本地没有但远端有：补齐父链后插入
func RefreshNode(ctx context.Context, idx *index.Index, st NodeSource, id int) (string, bool, error) {
	local, known, err := idx.NodeByID(ctx, id)
	if err != nil {
		return "", false, err
	}
	metas, err := st.FetchNodes(ctx, []int{id})
	if err != nil {
		return "", false, err
	}
	info, exists := metas[id]
	if !exists {
		if !known {
			return "", false, nil
		}
		if _, derr := idx.DeleteSubtree(ctx, id); derr != nil {
			return local.Path, false, derr
		}
		return local.Path, false, nil
	}
	pid, err := ensureParent(ctx, idx, st, info.Parent)
	if err != nil {
		return "", false, err
	}
	if !known {
		p := joinPath(dirPath(ctx, idx, pid), info.Name)
		return p, true, idx.PutNode(ctx, index.Node{
			ID: info.MessageID, ParentID: pid, Name: info.Name, IsDir: info.IsDir,
			Path: p, DocumentID: info.DocumentID, AccessHash: info.AccessHash,
			FileReference: info.FileReference, Size: info.Size, Mtime: info.Mtime,
			Sha256: info.Sha,
		})
	}
	if local.ParentID != pid || local.Name != info.Name {
		_, np, merr := idx.MoveNode(ctx, id, pid, info.Name)
		if merr != nil {
			return local.Path, true, merr
		}
		local.Path = np
	}
	local.ParentID = pid
	local.Name = info.Name
	local.DocumentID = info.DocumentID
	local.AccessHash = info.AccessHash
	local.FileReference = info.FileReference
	local.Size = info.Size
	local.Mtime = info.Mtime
	local.Sha256 = info.Sha
	return local.Path, true, idx.PutNode(ctx, local)
}

// ApplyChange 应用一条「消息被新增或编辑」的差异事件。
//
// 输入：ctx；idx；st；id 消息号
// 输出：变更路径、移除路径、是否识别、error
// 预期：不是本程序节点消息时 handled=false，调用方忽略即可
func ApplyChange(ctx context.Context, idx *index.Index, st NodeSource, id int) (changed, removed []string, handled bool, err error) {
	_, known, cerr := idx.NodeByID(ctx, id)
	if cerr != nil {
		return nil, nil, false, cerr
	}
	metas, ferr := st.FetchNodes(ctx, []int{id})
	if ferr != nil {
		return nil, nil, false, ferr
	}
	if _, exists := metas[id]; !exists && !known {
		return nil, nil, false, nil
	}
	p, exists, perr := RefreshNode(ctx, idx, st, id)
	if perr != nil {
		return nil, nil, false, perr
	}
	if !exists {
		return nil, []string{p}, true, nil
	}
	return []string{p}, nil, true, nil
}

// DeleteRemote 应用一条删除事件。
//
// 输入：ctx；idx；id 被删消息号
// 输出：移除路径、是否识别、error
// 预期：节点被删时整棵子树从本地移除；未知消息忽略
func DeleteRemote(ctx context.Context, idx *index.Index, id int) (removed []string, handled bool, err error) {
	if _, ok, cerr := idx.NodeByID(ctx, id); cerr != nil {
		return nil, false, cerr
	} else if !ok {
		return nil, false, nil
	}
	removed = pathsOf(ctx, idx, id)
	_, derr := idx.DeleteSubtree(ctx, id)
	return removed, true, derr
}

// ensureParent 补齐全新节点的父链，返回可用的父消息号。
//
// 输入：ctx；idx；st；parentID 目标父消息号
// 输出：本地可用的父消息号（父链缺失时为 0）；error
// 预期：沿父链上溯最多 maxParentHops 层，成环即停在缺失处挂根
func ensureParent(ctx context.Context, idx *index.Index, st NodeSource, parentID int) (int, error) {
	if parentID <= 0 {
		return 0, nil
	}
	if _, ok, err := idx.NodeByID(ctx, parentID); err != nil {
		return 0, err
	} else if ok {
		return parentID, nil
	}
	infos := map[int]telegramstore.NodeInfo{}
	order := make([]int, 0, 4)
	seen := map[int]bool{}
	cur := parentID
	for range maxParentHops {
		if cur <= 0 || seen[cur] {
			break
		}
		seen[cur] = true
		if _, ok, err := idx.NodeByID(ctx, cur); err != nil {
			return 0, err
		} else if ok {
			break
		}
		metas, err := st.FetchNodes(ctx, []int{cur})
		if err != nil {
			return 0, err
		}
		info, ok := metas[cur]
		if !ok {
			break
		}
		infos[cur] = info
		order = append(order, cur)
		cur = info.Parent
	}
	// 从最靠根的一层往下插入，保证派生路径可算
	for i := len(order) - 1; i >= 0; i-- {
		info := infos[order[i]]
		pid := info.Parent
		if pid != 0 {
			if _, ok, _ := idx.NodeByID(ctx, pid); !ok {
				pid = 0
			}
		}
		if err := idx.PutNode(ctx, index.Node{
			ID: info.MessageID, ParentID: pid, Name: info.Name, IsDir: info.IsDir,
			DocumentID: info.DocumentID, AccessHash: info.AccessHash,
			FileReference: info.FileReference, Size: info.Size, Mtime: info.Mtime,
			Sha256: info.Sha,
		}); err != nil {
			return 0, err
		}
	}
	if _, ok, _ := idx.NodeByID(ctx, parentID); ok {
		return parentID, nil
	}
	return 0, nil
}

// dirPath 返回父目录的派生路径；父不存在时按卷根。
func dirPath(ctx context.Context, idx *index.Index, parentID int) string {
	if parentID <= 0 {
		return "/"
	}
	if n, ok, err := idx.NodeByID(ctx, parentID); err == nil && ok {
		return n.Path
	}
	return "/"
}

// pathsOf 返回节点自己与全部后代的路径，供上层刷新界面。
func pathsOf(ctx context.Context, idx *index.Index, id int) []string {
	ids, err := idx.SubtreeIDs(ctx, id)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(ids))
	for _, v := range ids {
		if n, ok, err := idx.NodeByID(ctx, v); err == nil && ok {
			out = append(out, n.Path)
		}
	}
	slices.Sort(out)
	return out
}

// joinPath 拼父路径与名字。
func joinPath(parent, name string) string {
	if parent == "" || parent == "/" {
		return "/" + name
	}
	return parent + "/" + name
}
