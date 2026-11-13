package fsck

// 索引对账单测：以远端全量节点为准，报告 missing/moved/added 并可选重建。

import (
	"context"
	"testing"

	"mytgfs/internal/index"
	"mytgfs/internal/telegramstore"
)

// fakeSource 返回内存里的远端全量节点。
type fakeSource struct {
	nodes []telegramstore.NodeInfo
	err   error
}

func (f *fakeSource) AllNodes(context.Context) ([]telegramstore.NodeInfo, error) {
	return f.nodes, f.err
}

func newTestIndex(t *testing.T) *index.Index {
	t.Helper()
	idx, err := index.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = idx.Close() })
	return idx
}

func baseNodes() []telegramstore.NodeInfo {
	return []telegramstore.NodeInfo{
		{MessageID: 1, Name: "dir", Parent: 0, IsDir: true},
		{MessageID: 2, Name: "a.bin", Parent: 1, Size: 3, Sha: "aa"},
	}
}

// TestRunClean 本地与远端一致时零问题。
func TestRunClean(t *testing.T) {
	ctx := t.Context()
	idx := newTestIndex(t)
	putNodes(t, idx, baseNodes())
	rep, err := Run(ctx, idx, &fakeSource{nodes: baseNodes()}, false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.Checked != 2 || len(rep.Problems) != 0 {
		t.Fatalf("不该有问题: %+v", rep)
	}
}

// TestRunMissing 远端删了节点 => KindMissing。
func TestRunMissing(t *testing.T) {
	ctx := t.Context()
	idx := newTestIndex(t)
	putNodes(t, idx, baseNodes())
	remote := []telegramstore.NodeInfo{baseNodes()[0]}
	rep, err := Run(ctx, idx, &fakeSource{nodes: remote}, false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.Missing != 1 || len(rep.Problems) != 1 || rep.Problems[0].Kind != KindMissing {
		t.Fatalf("应报 1 条 missing: %+v", rep)
	}
}

// TestRunMoved 远端改名 => KindMoved 带实际路径。
func TestRunMoved(t *testing.T) {
	ctx := t.Context()
	idx := newTestIndex(t)
	putNodes(t, idx, baseNodes())
	remote := []telegramstore.NodeInfo{
		{MessageID: 1, Name: "renamed", Parent: 0, IsDir: true},
		{MessageID: 2, Name: "a.bin", Parent: 1, Size: 3, Sha: "aa"},
	}
	rep, err := Run(ctx, idx, &fakeSource{nodes: remote}, false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.Moved != 2 || rep.Problems[0].Kind != KindMoved {
		t.Fatalf("改名应报 moved: %+v", rep)
	}
}

// TestRunAdded 远端有本地没有 => KindAdded。
func TestRunAdded(t *testing.T) {
	ctx := t.Context()
	idx := newTestIndex(t)
	putNodes(t, idx, baseNodes())
	remote := append([]telegramstore.NodeInfo{}, baseNodes()...)
	remote = append(remote, telegramstore.NodeInfo{MessageID: 5, Name: "new.bin", Parent: 0, Size: 1, Sha: "bb"})
	rep, err := Run(ctx, idx, &fakeSource{nodes: remote}, false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.Added != 1 {
		t.Fatalf("应报 1 条 added: %+v", rep)
	}
}

// TestRunFixRebuilds fix 时按远端重建，问题清零。
func TestRunFixRebuilds(t *testing.T) {
	ctx := t.Context()
	idx := newTestIndex(t)
	putNodes(t, idx, baseNodes())
	remote := []telegramstore.NodeInfo{
		{MessageID: 1, Name: "renamed", Parent: 0, IsDir: true},
		{MessageID: 2, Name: "a.bin", Parent: 1, Size: 3, Sha: "aa"},
	}
	rep, err := Run(ctx, idx, &fakeSource{nodes: remote}, true)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.Fixed == 0 {
		t.Fatalf("fix 应记为已修复: %+v", rep)
	}
	if _, ok, _ := idx.NodeByPath(ctx, "/renamed/a.bin"); !ok {
		t.Fatal("重建后应出现远端路径")
	}
}

// TestRunSourceError 远端拉取失败原样返回。
func TestRunSourceError(t *testing.T) {
	idx := newTestIndex(t)
	if _, err := Run(t.Context(), idx, &fakeSource{err: context.DeadlineExceeded}, false); err == nil {
		t.Fatal("远端错误应返回")
	}
}

// putNodes 按全量节点布局写进本地索引。
func putNodes(t *testing.T, idx *index.Index, nodes []telegramstore.NodeInfo) {
	t.Helper()
	ctx := t.Context()
	for _, n := range nodes {
		path := "/" + n.Name
		if n.Parent != 0 {
			for _, p := range nodes {
				if p.MessageID == n.Parent {
					path = "/" + p.Name + path
					break
				}
			}
		}
		if err := idx.PutNode(ctx, index.Node{
			ID: n.MessageID, ParentID: n.Parent, Name: n.Name, IsDir: n.IsDir,
			Path: path, Size: n.Size, Sha256: n.Sha,
		}); err != nil {
			t.Fatal(err)
		}
	}
}
