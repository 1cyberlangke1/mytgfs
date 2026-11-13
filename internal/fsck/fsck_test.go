package fsck

// 索引对账测试：索引行对应的频道消息被删或 caption 里的路径变了，都要能被发现并修复。

import (
	"context"
	"testing"

	"github.com/gotd/td/tg"

	"mytgfs/internal/index"
)

// fakeAPI 按请求的消息 ID 返回预置结果。
type fakeAPI struct {
	msgs map[int]tg.MessageClass
	// 便于断言请求了多少 ID
	requested []int
}

func (f *fakeAPI) ChannelsGetMessages(_ context.Context, req *tg.ChannelsGetMessagesRequest) (tg.MessagesMessagesClass, error) {
	out := &tg.MessagesChannelMessages{}
	for _, id := range req.ID {
		if m, ok := id.(*tg.InputMessageID); ok {
			f.requested = append(f.requested, m.ID)
			if mc, exist := f.msgs[m.ID]; exist {
				out.Messages = append(out.Messages, mc)
			}
		}
	}
	return out, nil
}

// fileMsg 造一条带 caption 的文件消息。
func fileMsg(id int, caption string) *tg.Message {
	return &tg.Message{ID: id, Message: caption}
}

// TestRunReportsMissing 频道消息已被删除的索引行报缺失。
func TestRunReportsMissing(t *testing.T) {
	idx := newIdx(t)
	ctx := context.Background()
	put(t, idx, index.File{Path: "/a.bin", MessageID: 1, Size: 1})
	put(t, idx, index.File{Path: "/b.bin", MessageID: 2, Size: 1})

	api := &fakeAPI{msgs: map[int]tg.MessageClass{
		1: fileMsg(1, `{"path":"/a.bin"}`),
		2: &tg.MessageEmpty{ID: 2},
	}}
	rep, err := Run(ctx, idx, api, 100, 200, false)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Checked != 2 || rep.Missing != 1 || rep.Moved != 0 || rep.Fixed != 0 {
		t.Fatalf("统计不符: %+v", rep)
	}
	if len(rep.Problems) != 1 || rep.Problems[0].Path != "/b.bin" || rep.Problems[0].Kind != KindMissing {
		t.Fatalf("问题项不符: %+v", rep.Problems)
	}
	if _, ok, _ := idx.Get(ctx, "/b.bin"); !ok {
		t.Fatal("未加 --fix 时不应修改索引")
	}
}

// TestRunFixesMissing 加修复时删掉缺失行，其它行保留。
func TestRunFixesMissing(t *testing.T) {
	idx := newIdx(t)
	ctx := context.Background()
	put(t, idx, index.File{Path: "/a.bin", MessageID: 1, Size: 1})
	put(t, idx, index.File{Path: "/b.bin", MessageID: 2, Size: 1})

	api := &fakeAPI{msgs: map[int]tg.MessageClass{
		1: fileMsg(1, `{"path":"/a.bin"}`),
	}}
	rep, err := Run(ctx, idx, api, 100, 200, true)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Fixed != 1 {
		t.Fatalf("应修复 1 条，实际 %+v", rep)
	}
	if _, ok, _ := idx.Get(ctx, "/b.bin"); ok {
		t.Fatal("缺失行应删除")
	}
	if _, ok, _ := idx.Get(ctx, "/a.bin"); !ok {
		t.Fatal("正常行不应被改动")
	}
}

// TestRunReportsMoved caption 里的路径与索引不一致时报路径变化。
func TestRunReportsMoved(t *testing.T) {
	idx := newIdx(t)
	ctx := context.Background()
	put(t, idx, index.File{Path: "/old.bin", MessageID: 5, DocumentID: 9, Size: 3, Sha256: "aa"})

	api := &fakeAPI{msgs: map[int]tg.MessageClass{
		5: fileMsg(5, `{"path":"/new.bin","sha256":"aa"}`),
	}}
	rep, err := Run(ctx, idx, api, 100, 200, false)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Moved != 1 || rep.Missing != 0 {
		t.Fatalf("统计不符: %+v", rep)
	}
	if rep.Problems[0].Actual != "/new.bin" {
		t.Fatalf("应报出 caption 里的新路径，实际 %+v", rep.Problems[0])
	}
}

// TestRunFixesMoved 修复时把索引行迁到 caption 记录的路径。
func TestRunFixesMoved(t *testing.T) {
	idx := newIdx(t)
	ctx := context.Background()
	put(t, idx, index.File{Path: "/old.bin", MessageID: 5, DocumentID: 9, Size: 3, Sha256: "aa"})

	api := &fakeAPI{msgs: map[int]tg.MessageClass{
		5: fileMsg(5, `{"path":"/new.bin","sha256":"aa"}`),
	}}
	rep, err := Run(ctx, idx, api, 100, 200, true)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Fixed != 1 {
		t.Fatalf("应修复 1 条，实际 %+v", rep)
	}
	if _, ok, _ := idx.Get(ctx, "/old.bin"); ok {
		t.Fatal("旧路径行应删除")
	}
	got, ok, _ := idx.Get(ctx, "/new.bin")
	if !ok || got.MessageID != 5 || got.DocumentID != 9 || got.Sha256 != "aa" {
		t.Fatalf("应迁到新路径: ok=%v entry=%+v", ok, got)
	}
}

// TestRunClean 一致时没有问题项。
func TestRunClean(t *testing.T) {
	idx := newIdx(t)
	ctx := context.Background()
	put(t, idx, index.File{Path: "/a.bin", MessageID: 1, Size: 1})
	api := &fakeAPI{msgs: map[int]tg.MessageClass{
		1: fileMsg(1, `{"path":"/a.bin"}`),
	}}
	rep, err := Run(ctx, idx, api, 100, 200, false)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Checked != 1 || rep.Missing != 0 || rep.Moved != 0 || len(rep.Problems) != 0 {
		t.Fatalf("应一致: %+v", rep)
	}
}

// TestRunBatches 超过一批时按批请求，不丢行。
func TestRunBatches(t *testing.T) {
	idx := newIdx(t)
	ctx := context.Background()
	msgs := map[int]tg.MessageClass{}
	for i := 1; i <= 250; i++ {
		put(t, idx, index.File{Path: pathOf(i), MessageID: i, Size: 1})
		msgs[i] = fileMsg(i, `{"path":"`+pathOf(i)+`"}`)
	}
	api := &fakeAPI{msgs: msgs}
	rep, err := Run(ctx, idx, api, 100, 200, false)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Checked != 250 || len(rep.Problems) != 0 {
		t.Fatalf("应全部一致: checked=%d problems=%+v", rep.Checked, rep.Problems)
	}
	if len(api.requested) != 250 {
		t.Fatalf("应请求 250 个 ID，实际 %d", len(api.requested))
	}
}

func newIdx(t *testing.T) *index.Index {
	t.Helper()
	idx, err := index.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { idx.Close() })
	return idx
}

func put(t *testing.T, idx *index.Index, f index.File) {
	t.Helper()
	if err := idx.Put(context.Background(), f); err != nil {
		t.Fatal(err)
	}
}

func pathOf(i int) string { return "/f" + itoa(i) + ".bin" }

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
