package trayapp

// 自动增量同步的单测：用假差异源，不连 Telegram。
//
// 覆盖两件事：
//  1. 首轮差异里的新消息会写进索引，并触发一次变更回调
//  2. 回调为 nil 时（纯面板模式）不 panic，取消后能及时退出

import (
	"context"
	"sync"
	"testing"
	"time"

	"mytgfs/internal/index"
	tgsync "mytgfs/internal/sync"
)

// fakeDiffer 首轮返回一条文件消息，之后返回空差异。
type fakeDiffer struct {
	mu    sync.Mutex
	calls int
}

func (f *fakeDiffer) ChannelDifference(_ context.Context, _, _ int) (tgsync.Difference, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.calls == 1 {
		return tgsync.Difference{
			Pts:   10,
			Final: true,
			Messages: []tgsync.Message{{
				ID:            1,
				Caption:       tgsync.EncodeCaptionHash("/a.txt", ""),
				DocumentID:    7,
				AccessHash:    8,
				FileReference: []byte{1},
				Size:          3,
				Date:          time.Unix(1700000000, 0),
			}},
		}, nil
	}
	return tgsync.Difference{Pts: 10, Final: true}, nil
}

// TestAutoSyncAppliesAndNotifies 首轮写入索引并回调变更路径。
func TestAutoSyncAppliesAndNotifies(t *testing.T) {
	idx, err := index.OpenMemory()
	if err != nil {
		t.Fatalf("打开索引失败: %v", err)
	}
	defer idx.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	notified := make(chan struct{}, 1)
	var paths []string
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		AutoSync(ctx, func(changed, _ []string) {
			paths = changed
			select {
			case notified <- struct{}{}:
			default:
			}
		}, idx, &fakeDiffer{}, 20*time.Millisecond, nil)
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
		t.Fatal("取消后 AutoSync 没有退出")
	}

	f, ok, err := idx.Get(context.Background(), "/a.txt")
	if err != nil || !ok {
		t.Fatalf("索引里没有 /a.txt: ok=%v err=%v", ok, err)
	}
	if f.MessageID != 1 || f.Size != 3 {
		t.Fatalf("索引内容不符: %+v", f)
	}
	if len(paths) != 1 || paths[0] != "/a.txt" {
		t.Fatalf("回调路径不符: %v", paths)
	}
}

// TestAutoSyncNilNotify 没有挂载点时回调可省略。
func TestAutoSyncNilNotify(t *testing.T) {
	idx, err := index.OpenMemory()
	if err != nil {
		t.Fatalf("打开索引失败: %v", err)
	}
	defer idx.Close()

	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		AutoSync(ctx, nil, idx, &fakeDiffer{}, 20*time.Millisecond, nil)
	}()

	// 等首轮同步落库，再取消
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok, _ := idx.Get(context.Background(), "/a.txt"); ok {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("取消后 AutoSync 没有退出")
	}
	if _, ok, _ := idx.Get(context.Background(), "/a.txt"); !ok {
		t.Fatal("首轮同步没有写入索引")
	}
}
