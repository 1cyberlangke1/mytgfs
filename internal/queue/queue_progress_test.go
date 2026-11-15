package queue

// 进度与上传车道的单测：不联网、不测时钟，只用通道与确定顺序判并发。

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// TestReportProgressInSnapshot 验证进度写入快照且不倒退。
//
// 输入：一条排队的上传任务，先后上报 512KB 与 0
// 输出：Snapshot 里 done 取较大值，size 保留
// 预期：面板靠快照显示百分比，倒退会让进度条来回跳
func TestReportProgressInSnapshot(t *testing.T) {
	q := newQuietQueue(t)
	ctx := t.Context()
	id, err := q.Enqueue(ctx, Job{Volume: "demo", Kind: KindUpload, Name: "/a.bin"})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	const part = 512 << 10
	q.Report(id, part, 3*part)
	items := q.Snapshot()
	if len(items) != 1 {
		t.Fatalf("快照条数 = %d, 期望 1", len(items))
	}
	if items[0].Done != part || items[0].Size != 3*part {
		t.Fatalf("进度 = %d/%d, 期望 %d/%d", items[0].Done, items[0].Size, part, 3*part)
	}
	// 续传回到早期分片时会报更小的值：进度条不能倒退
	q.Report(id, 0, 3*part)
	if got := q.Snapshot()[0]; got.Done != part {
		t.Fatalf("倒退后的进度 = %d, 期望仍为 %d", got.Done, part)
	}
}

// TestUploadLaneRunsThreeParallel 验证同卷最多 3 个上传任务同时推进。
//
// 输入：同一卷的 4 条上传任务；handler 阻塞到被放行
// 输出：先启动 3 条，放行其中一条后第 4 条才启动
// 预期：车道上限是结构性并行，不是按某次测速调的数字
func TestUploadLaneRunsThreeParallel(t *testing.T) {
	q := newTestQueue(t, 8)
	ctx := t.Context()
	started := make(chan int64, 8)
	release := make(chan int64, 8)
	var active atomic.Int64
	var maxActive atomic.Int64
	q.SetHandler(func(_ context.Context, j Job) error {
		n := active.Add(1)
		for {
			old := maxActive.Load()
			if n <= old || maxActive.CompareAndSwap(old, n) {
				break
			}
		}
		started <- j.ID
		<-release
		active.Add(-1)
		return nil
	})

	var ids []int64
	for range 4 {
		id, err := q.Enqueue(ctx, Job{Volume: "demo", Kind: KindUpload, Name: "/f"})
		if err != nil {
			t.Fatalf("enqueue: %v", err)
		}
		ids = append(ids, id)
	}

	// 先到 3 条：这是车道上限
	first := <-started
	<-started
	<-started
	if got := maxActive.Load(); got != uploadLane {
		t.Fatalf("同时启动 %d 条, 期望 %d", got, uploadLane)
	}
	// 放行一条：腾出的名额必须让第 4 条起来
	release <- first
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("放行一条后第 4 条上传没有启动")
	}
	if got := maxActive.Load(); got != uploadLane {
		t.Fatalf("换人后同时运行 %d 条, 期望 %d", got, uploadLane)
	}
	for range 3 {
		release <- 0
	}
	for _, id := range ids {
		if err := q.Wait(ctx, id); err != nil {
			t.Fatalf("wait %d: %v", id, err)
		}
	}
}

// TestMetadataJobWaitsForUploads 验证同卷元数据操作不会被上传插队。
//
// 输入：一条上传 + 一条 mkdir，同一卷
// 输出：mkdir 等上传结束才开始
// 预期：改名/删除这类操作要保持互斥，只有上传走并行车道
func TestMetadataJobWaitsForUploads(t *testing.T) {
	q := newTestQueue(t, 4)
	ctx := t.Context()
	started := make(chan string, 4)
	releaseUpload := make(chan struct{})
	q.SetHandler(func(_ context.Context, j Job) error {
		started <- j.Kind
		if j.Kind == KindUpload {
			<-releaseUpload
		}
		return nil
	})

	up, err := q.Enqueue(ctx, Job{Volume: "demo", Kind: KindUpload, Name: "/f"})
	if err != nil {
		t.Fatalf("enqueue upload: %v", err)
	}
	if kind := <-started; kind != KindUpload {
		t.Fatalf("首个任务 = %s, 期望 upload", kind)
	}
	mk, err := q.Enqueue(ctx, Job{Volume: "demo", Kind: KindMkdir, Name: "/d"})
	if err != nil {
		t.Fatalf("enqueue mkdir: %v", err)
	}
	select {
	case kind := <-started:
		t.Fatalf("%s 在上传未结束时启动", kind)
	case <-time.After(300 * time.Millisecond):
		// 期望：没有第二个任务启动
	}
	close(releaseUpload)
	if kind := <-started; kind != KindMkdir {
		t.Fatalf("上传结束后启动 %s, 期望 mkdir", kind)
	}
	for _, id := range []int64{up, mk} {
		if err := q.Wait(ctx, id); err != nil {
			t.Fatalf("wait %d: %v", id, err)
		}
	}
}
