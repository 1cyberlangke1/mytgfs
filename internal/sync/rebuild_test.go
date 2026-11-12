package sync

// 索引重建的翻页逻辑单元测试：不连网，用假分页器喂预置页。

import (
	"context"
	"errors"
	"testing"

	"mytgfs/internal/index"
)

// fakePager 按 offsetID 返回预置页与下一页游标。
type fakePager struct {
	pages map[int][]Message
	next  map[int]int
	errAt map[int]error
}

func (f fakePager) HistoryBefore(_ context.Context, offsetID, _ int) ([]Message, int, error) {
	if err := f.errAt[offsetID]; err != nil {
		return nil, 0, err
	}
	return f.pages[offsetID], f.next[offsetID], nil
}

// TestRebuildPagesBackwards 倒序翻页直到游标为 0，所有文件都进索引。
//
// 输入：三页，游标 0 -> 5 -> 1 -> 0
// 输出：索引内容、Rebuild 返回的消息数
// 预期：三条文件都在索引里，消息数为 3
func TestRebuildPagesBackwards(t *testing.T) {
	ctx := context.Background()
	idx, err := index.OpenMemory()
	if err != nil {
		t.Fatalf("打开索引: %v", err)
	}
	defer idx.Close()

	p := fakePager{
		pages: map[int][]Message{
			0: {
				{ID: 10, Caption: EncodeCaptionHash("/a.txt", "aa"), Size: 1},
				{ID: 8, Caption: EncodeCaption("/b.txt"), Size: 2},
			},
			5: {{ID: 3, Caption: EncodeCaption("/c.txt"), Size: 3}},
		},
		next: map[int]int{0: 5, 5: 1, 1: 0},
	}

	n, err := Rebuild(ctx, idx, p, 100, 10)
	if err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	if n != 3 {
		t.Fatalf("应重放 3 条，实际 %d", n)
	}
	for _, want := range []string{"/a.txt", "/b.txt", "/c.txt"} {
		if _, ok, gerr := idx.Get(ctx, want); gerr != nil || !ok {
			t.Fatalf("索引缺少 %s: ok=%v err=%v", want, ok, gerr)
		}
	}
	f, _, _ := idx.Get(ctx, "/a.txt")
	if f.MessageID != 10 || f.Sha256 != "aa" {
		t.Fatalf("消息号或摘要不符: %+v", f)
	}
}

// TestRebuildStopsOnStalledCursor 游标不再前进时必须停止，防止死循环。
//
// 输入：第二页的游标等于当前 offset
// 输出：Rebuild 返回
// 预期：不报错，只重放第一页
func TestRebuildStopsOnStalledCursor(t *testing.T) {
	ctx := context.Background()
	idx, _ := index.OpenMemory()
	defer idx.Close()

	p := fakePager{
		pages: map[int][]Message{
			0: {{ID: 9, Caption: EncodeCaption("/a.txt"), Size: 1}},
			9: {{ID: 9, Caption: EncodeCaption("/a.txt"), Size: 1}},
		},
		next: map[int]int{0: 9, 9: 9},
	}
	n, err := Rebuild(ctx, idx, p, 100, 10)
	if err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	if n != 1 {
		t.Fatalf("游标停住后应停止，实际重放 %d", n)
	}
}

// TestRebuildReturnsError 翻页失败要向上返回，不能吞掉。
func TestRebuildReturnsError(t *testing.T) {
	ctx := context.Background()
	idx, _ := index.OpenMemory()
	defer idx.Close()

	p := fakePager{
		pages: map[int][]Message{},
		next:  map[int]int{},
		errAt: map[int]error{0: errors.New("rpc down")},
	}
	if _, err := Rebuild(ctx, idx, p, 100, 10); err == nil {
		t.Fatal("应返回翻页错误")
	}
}

// TestRebuildRespectsMaxPages 页数上限要生效，避免一次拉爆。
func TestRebuildRespectsMaxPages(t *testing.T) {
	ctx := context.Background()
	idx, _ := index.OpenMemory()
	defer idx.Close()

	p := fakePager{
		pages: map[int][]Message{
			0: {{ID: 10, Caption: EncodeCaption("/a.txt"), Size: 1}},
			9: {{ID: 9, Caption: EncodeCaption("/b.txt"), Size: 1}},
		},
		next: map[int]int{0: 9, 9: 1},
	}
	n, err := Rebuild(ctx, idx, p, 100, 1)
	if err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	if n != 1 {
		t.Fatalf("maxPages=1 只应重放 1 条，实际 %d", n)
	}
}
