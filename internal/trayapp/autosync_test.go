package trayapp

// 自动增量同步的单测：用假差异源 + 假节点源，不连 Telegram。

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"mytgfs/internal/index"
	tgsync "mytgfs/internal/sync"
	"mytgfs/internal/telegramstore"
)

// fakeDiffer 首轮返回一条节点变更事件，之后返回空差异。
type fakeDiffer struct {
	mu    sync.Mutex
	calls int
}

func (f *fakeDiffer) ChannelDifference(_ context.Context, _, _ int) (tgsync.Difference, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.calls == 1 {
		return tgsync.Difference{Pts: 10, Final: true, Changed: []int{2}}, nil
	}
	return tgsync.Difference{Pts: 10, Final: true}, nil
}

// fakeNodes 是内存里的远端节点源：消息 2 是 /a.txt。
type fakeNodes struct{}

func (fakeNodes) FetchNodes(_ context.Context, ids []int) (map[int]telegramstore.NodeInfo, error) {
	out := map[int]telegramstore.NodeInfo{}
	for _, id := range ids {
		if id == 2 {
			out[id] = telegramstore.NodeInfo{
				MessageID: 2, Name: "a.txt", Parent: 0,
				DocumentID: 7, AccessHash: 8, Size: 3, Sha: "aa",
			}
		}
	}
	return out, nil
}

// newTestIndex 建空的内存索引；卷根是虚拟的，不需要根消息。
func newTestIndex(t *testing.T) *index.Index {
	t.Helper()
	idx, err := index.OpenMemory()
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	t.Cleanup(func() { _ = idx.Close() })
	return idx
}

// TestAutoSyncAppliesAndNotifies 首轮写入索引并回调变更路径。
func TestAutoSyncAppliesAndNotifies(t *testing.T) {
	idx := newTestIndex(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	notified := make(chan struct{}, 1)
	var paths []string
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		AutoSyncWhile(ctx, func(changed, _ []string) {
			paths = changed
			select {
			case notified <- struct{}{}:
			default:
			}
		}, idx, &fakeDiffer{}, fakeNodes{}, 20*time.Millisecond, nil, nil)
	}()

	select {
	case <-notified:
	case <-time.After(3 * time.Second):
		t.Fatal("三秒内没有收到变更回调")
	}
	cancel()
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("取消后 AutoSyncWhile 没有退出")
	}

	n, ok, err := idx.NodeByPath(context.Background(), "/a.txt")
	if err != nil || !ok {
		t.Fatalf("索引里没有 /a.txt: ok=%v err=%v", ok, err)
	}
	if n.ID != 2 || n.Size != 3 {
		t.Fatalf("索引内容不符: %+v", n)
	}
	if len(paths) != 1 || paths[0] != "/a.txt" {
		t.Fatalf("回调路径不符: %v", paths)
	}
}

// coverNodes 在 fakeNodes 基础上报告一次头像变化。
type coverNodes struct {
	fakeNodes
	calls int
}

func (c *coverNodes) CoverChanged(context.Context) (bool, error) {
	c.calls++
	return c.calls == 1, nil
}

// TestAutoSyncNotifiesCoverChange 头像变化用空列表通知一次。
//
// 输入：带 CoverChanged 的节点源
// 输出：notify 至少收到一次 changed/removed 都为空的调用
// 预期：封面不参与节点差异，但面板仍要收到刷新信号
func TestAutoSyncNotifiesCoverChange(t *testing.T) {
	idx := newTestIndex(t)
	ctx := t.Context()
	nodes := &coverNodes{}
	events := make(chan [2]int, 8)
	go AutoSyncWhile(ctx, func(changed, removed []string) {
		events <- [2]int{len(changed), len(removed)}
	}, idx, &fakeDiffer{}, nodes, 20*time.Millisecond, nil, nil)

	deadline := time.After(3 * time.Second)
	sawEmpty := false
	for range 3 {
		select {
		case e := <-events:
			if e[0] == 0 && e[1] == 0 {
				sawEmpty = true
			}
		case <-deadline:
			t.Fatal("三秒内没有收到通知")
		}
		if sawEmpty {
			return
		}
	}
	t.Fatal("没有收到空列表的封面变化通知")
}

// TestAutoSyncNilNotify 回调可省略。
func TestAutoSyncNilNotify(t *testing.T) {
	idx := newTestIndex(t)
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		AutoSyncWhile(ctx, nil, idx, &fakeDiffer{}, fakeNodes{}, 20*time.Millisecond, nil, nil)
	}()
	time.Sleep(60 * time.Millisecond)
	cancel()
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("取消后 AutoSyncWhile 没有退出")
	}
}

// TestAutoSyncWhileHonorsSwitch 关闭时不同步，打开后立即同步。
func TestAutoSyncWhileHonorsSwitch(t *testing.T) {
	idx := newTestIndex(t)
	ctx := t.Context()
	var on atomic.Bool
	d := &fakeDiffer{}
	go AutoSyncWhile(ctx, nil, idx, d, fakeNodes{}, 20*time.Millisecond, nil, on.Load)

	time.Sleep(60 * time.Millisecond)
	d.mu.Lock()
	closed := d.calls
	d.mu.Unlock()
	if closed != 0 {
		t.Fatalf("关闭时不该同步，实际 %d 轮", closed)
	}
	on.Store(true)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		d.mu.Lock()
		opened := d.calls
		d.mu.Unlock()
		if opened > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("打开后没有同步")
}
