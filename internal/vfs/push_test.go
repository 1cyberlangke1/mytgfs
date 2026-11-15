package vfs

// 推送约束单测：每一个会改动卷内容的操作都必须推 status。
//
// 这不是「顺手多测一下」：面板的操作队列、文件列表全靠这些推送刷新，
// 少一次用户就会看到「已经改完但界面没变」。

import (
	"testing"

	"mytgfs/internal/push"
)

// TestMutationsPushStatus 逐个写操作断言至少推送一次 status。
//
// 输入：建目录、上传、改名、删除各执行一次
// 输出：每次操作后记录器里都有 status 主题
// 预期：任何写路径漏掉 notify 都会在这里失败
func TestMutationsPushStatus(t *testing.T) {
	fsys, _, rec, cleanup := newTestFSWithEvents(t)
	defer cleanup()
	ctx := t.Context()

	steps := []struct {
		name string
		run  func() error
	}{
		{"mkdir", func() error { return fsys.Mkdir(ctx, "/d", 0o755) }},
		{"upload", func() error {
			putFile(t, fsys, "/d/a.txt", "hello")
			return nil
		}},
		{"rename", func() error { return fsys.Rename(ctx, "/d/a.txt", "/d/b.txt") }},
		{"remove", func() error { return fsys.RemoveAll(ctx, "/d/b.txt") }},
	}
	for _, step := range steps {
		rec.Reset()
		if err := step.run(); err != nil {
			t.Fatalf("%s: %v", step.name, err)
		}
		if got := rec.Count(push.TopicStatus); got == 0 {
			t.Fatalf("%s 没有推送 %s", step.name, push.TopicStatus)
		}
	}
}

// TestReadProgressPushesProgressTopic 下载进度只推 progress，不推 status。
//
// 输入：track 记录 1MB/4MB 的进度，再 untrack
// 输出：progress 计数增加，status 计数保持 0
// 预期：status 会让前端重拉整棵文件树，进度必须走独立主题
func TestReadProgressPushesProgressTopic(t *testing.T) {
	fsys, _, rec, cleanup := newTestFSWithEvents(t)
	defer cleanup()

	rec.Reset()
	fsys.track("/big.bin", 1<<20, 4<<20)
	if got := rec.Count(push.TopicProgress); got == 0 {
		t.Fatal("track 没有推送 progress")
	}
	if got := rec.Count(push.TopicStatus); got != 0 {
		t.Fatalf("track 误推了 status %d 次", got)
	}

	rec.Reset()
	fsys.untrack("/big.bin")
	if got := rec.Count(push.TopicProgress); got == 0 {
		t.Fatal("untrack 没有推送 progress")
	}
	if got := rec.Count(push.TopicStatus); got != 0 {
		t.Fatalf("untrack 误推了 status %d 次", got)
	}
}
