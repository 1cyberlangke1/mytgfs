package sync

// 频道增量同步单测：用假差异源 + 假节点源，不连 Telegram。
//
// 新模型下每条消息自述名字与父消息号，差异事件按消息号直接取节点内容。

import (
	"context"
	"errors"
	"testing"

	"github.com/gotd/td/tgerr"

	"mytgfs/internal/index"
	"mytgfs/internal/telegramstore"
)

// fakeDiffer 按顺序返回预设差异。
type fakeDiffer struct {
	diffs []Difference
	errs  []error
	i     int
}

func (f *fakeDiffer) ChannelDifference(context.Context, int, int) (Difference, error) {
	if f.i < len(f.errs) && f.errs[f.i] != nil {
		err := f.errs[f.i]
		f.i++
		return Difference{}, err
	}
	if f.i >= len(f.diffs) {
		return Difference{Final: true, Pts: 10}, nil
	}
	d := f.diffs[f.i]
	f.i++
	return d, nil
}

// fakeNodes 是内存里的远端节点表：消息号 → 节点内容。
type fakeNodes struct {
	nodes   map[int]telegramstore.NodeInfo
	fetchEr error
}

func newFakeNodes() *fakeNodes {
	return &fakeNodes{nodes: map[int]telegramstore.NodeInfo{}}
}

func (f *fakeNodes) FetchNodes(_ context.Context, ids []int) (map[int]telegramstore.NodeInfo, error) {
	if f.fetchEr != nil {
		return nil, f.fetchEr
	}
	out := map[int]telegramstore.NodeInfo{}
	for _, id := range ids {
		if n, ok := f.nodes[id]; ok {
			out[id] = n
		}
	}
	return out, nil
}

func (f *fakeNodes) put(id int, name string, parent int, dir bool, size int64, sha string) {
	f.nodes[id] = telegramstore.NodeInfo{
		MessageID: id, Name: name, Parent: parent, IsDir: dir,
		DocumentID: int64(id) * 10, AccessHash: int64(id) * 100,
		Size: size, Mtime: 1000 + int64(id), Sha: sha,
	}
}

func newTestIndex(t *testing.T) *index.Index {
	t.Helper()
	idx, err := index.OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	t.Cleanup(func() { _ = idx.Close() })
	return idx
}

// TestSyncBootstrapAppliesChanged 首次同步把新消息写进索引。
func TestSyncBootstrapAppliesChanged(t *testing.T) {
	ctx := t.Context()
	idx := newTestIndex(t)
	nodes := newFakeNodes()
	nodes.put(2, "a.txt", 0, false, 3, "aa")
	d := &fakeDiffer{diffs: []Difference{{Pts: 5, Final: true, Changed: []int{2}}}}

	res, err := SyncChannel(ctx, idx, d, nodes, 100)
	if err != nil {
		t.Fatalf("SyncChannel: %v", err)
	}
	if res.Pulled != 1 || res.Pts != 5 || len(res.Changed) != 1 || res.Changed[0] != "/a.txt" {
		t.Fatalf("结果不符: %+v", res)
	}
	n, ok, _ := idx.NodeByID(ctx, 2)
	if !ok || n.Name != "a.txt" || n.Size != 3 || n.Sha256 != "aa" {
		t.Fatalf("节点不符: %+v", n)
	}
	v, ok, _ := idx.GetMeta(ctx, MetaChannelPts)
	if !ok || v != 5 {
		t.Fatalf("pts 未落盘: %d %v", v, ok)
	}
}

// TestSyncResumesPts 第二轮换用本地已存的 pts。
func TestSyncResumesPts(t *testing.T) {
	ctx := t.Context()
	idx := newTestIndex(t)
	_ = idx.PutMeta(ctx, MetaChannelPts, 40)
	d := &fakeDiffer{diffs: []Difference{{Pts: 41, Final: true}}}
	if _, err := SyncChannel(ctx, idx, d, newFakeNodes(), 100); err != nil {
		t.Fatalf("SyncChannel: %v", err)
	}
	if v, _, _ := idx.GetMeta(ctx, MetaChannelPts); v != 41 {
		t.Fatalf("pts=%d", v)
	}
}

// TestSyncIgnoresChatMessages 非节点消息（聊天文本）不写索引也不计 pulled。
func TestSyncIgnoresChatMessages(t *testing.T) {
	ctx := t.Context()
	idx := newTestIndex(t)
	d := &fakeDiffer{diffs: []Difference{{Pts: 2, Final: true, Changed: []int{99}}}}
	res, err := SyncChannel(ctx, idx, d, newFakeNodes(), 100)
	if err != nil {
		t.Fatalf("SyncChannel: %v", err)
	}
	if res.Pulled != 0 || len(res.Changed) != 0 {
		t.Fatalf("聊天消息不该被应用: %+v", res)
	}
	n, _ := idx.CountFiles(ctx)
	if n != 0 {
		t.Fatalf("索引不该有文件: %d", n)
	}
}

// TestSyncRenameAppliesRemoteName 远端把节点改名，本地跟着改。
func TestSyncRenameAppliesRemoteName(t *testing.T) {
	ctx := t.Context()
	idx := newTestIndex(t)
	nodes := newFakeNodes()
	nodes.put(2, "old.txt", 0, false, 1, "aa")
	// 先手动写一条旧名字的本地节点，模拟上次同步结果
	_ = idx.PutNode(ctx, index.Node{ID: 2, Name: "old.txt"})
	nodes.nodes[2] = telegramstore.NodeInfo{MessageID: 2, Name: "new.txt", Parent: 0, Size: 1, Sha: "aa"}
	d := &fakeDiffer{diffs: []Difference{{Pts: 6, Final: true, Changed: []int{2}}}}

	if _, err := SyncChannel(ctx, idx, d, nodes, 100); err != nil {
		t.Fatalf("SyncChannel: %v", err)
	}
	if _, ok, _ := idx.NodeByPath(ctx, "/new.txt"); !ok {
		t.Fatal("改名没生效")
	}
	if _, ok, _ := idx.NodeByPath(ctx, "/old.txt"); ok {
		t.Fatal("旧名字应消失")
	}
}

// TestSyncDeleteRemovesSubtree 删除事件把节点连同子树从本地清掉。
func TestSyncDeleteRemovesSubtree(t *testing.T) {
	ctx := t.Context()
	idx := newTestIndex(t)
	_ = idx.PutNode(ctx, index.Node{ID: 1, Name: "dir", IsDir: true})
	_ = idx.PutNode(ctx, index.Node{ID: 2, ParentID: 1, Name: "f.txt", Path: "/dir/f.txt"})
	d := &fakeDiffer{diffs: []Difference{{Pts: 7, Final: true, Deleted: []int{1}}}}

	res, err := SyncChannel(ctx, idx, d, newFakeNodes(), 100)
	if err != nil {
		t.Fatalf("SyncChannel: %v", err)
	}
	if res.Deleted != 2 {
		t.Fatalf("应删 2 条路径，实际 %+v", res)
	}
	if _, ok, _ := idx.NodeByID(ctx, 2); ok {
		t.Fatal("子节点应被删")
	}
}

// TestSyncTooLongMarksTruncated 差异过大只标记，不自动重建。
func TestSyncTooLongMarksTruncated(t *testing.T) {
	idx := newTestIndex(t)
	d := &fakeDiffer{diffs: []Difference{{Pts: 9, TooLong: true, Final: true}}}
	res, err := SyncChannel(t.Context(), idx, d, newFakeNodes(), 100)
	if err != nil {
		t.Fatalf("SyncChannel: %v", err)
	}
	if !res.Truncated || res.Rounds != 1 {
		t.Fatalf("应标记 Truncated: %+v", res)
	}
}

// TestSyncPtsInvalidRetries pts 失效时从 bootstrap 重来一次。
func TestSyncPtsInvalidRetries(t *testing.T) {
	ctx := t.Context()
	idx := newTestIndex(t)
	_ = idx.PutMeta(ctx, MetaChannelPts, 77)
	// 第一轮报 PERSISTENT_TIMESTAMP_INVALID，第二轮正常
	d := &fakeDiffer{errs: []error{tgerr.New(400, "PERSISTENT_TIMESTAMP_INVALID")}, diffs: []Difference{{Pts: 1, Final: true}}}
	res, err := SyncChannel(ctx, idx, d, newFakeNodes(), 100)
	if err != nil {
		t.Fatalf("SyncChannel: %v", err)
	}
	if res.Rounds != 1 {
		t.Fatalf("应重来一轮，实际 %+v", res)
	}
}

// TestSyncFetchErrorPropagates 取节点失败原样返回。
func TestSyncFetchErrorPropagates(t *testing.T) {
	idx := newTestIndex(t)
	nodes := newFakeNodes()
	nodes.fetchEr = errors.New("rpc down")
	d := &fakeDiffer{diffs: []Difference{{Pts: 3, Final: true, Changed: []int{2}}}}
	if _, err := SyncChannel(t.Context(), idx, d, nodes, 100); err == nil {
		t.Fatal("取节点失败应返回错误")
	}
}

// TestIsPtsInvalid 只认两种失效码。
func TestIsPtsInvalid(t *testing.T) {
	if IsPtsInvalid(nil) {
		t.Fatal("nil 不该算失效")
	}
	if IsPtsInvalid(errors.New("other")) {
		t.Fatal("普通错误不该算失效")
	}
}

// TestApplyChangeOnDeletedNode 变更事件对应的消息已删时，本地节点被清掉。
func TestApplyChangeOnDeletedNode(t *testing.T) {
	ctx := t.Context()
	idx := newTestIndex(t)
	_ = idx.PutNode(ctx, index.Node{ID: 5, Name: "gone.bin"})
	_, removed, handled, err := ApplyChange(ctx, idx, newFakeNodes(), 5)
	if err != nil || !handled || len(removed) != 1 || removed[0] != "/gone.bin" {
		t.Fatalf("ApplyChange=(%v,%v,%v)", removed, handled, err)
	}
	if _, ok, _ := idx.NodeByID(ctx, 5); ok {
		t.Fatal("本地节点应被删")
	}
}
