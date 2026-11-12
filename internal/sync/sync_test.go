package sync

import (
	"context"
	"testing"
	"time"

	"mytgfs/internal/index"
)

func TestReplayLatestWins(t *testing.T) {
	idx, err := index.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	ctx := context.Background()
	msgs := []Message{
		{ID: 2, Caption: `{"path":"/a.txt"}`, DocumentID: 20, Size: 200, Date: time.Unix(1000, 0)},
		{ID: 1, Caption: `{"path":"/a.txt"}`, DocumentID: 10, Size: 100, Date: time.Unix(900, 0)},
		{ID: 3, Caption: `not a caption`, DocumentID: 30},
	}
	if paths, err := Replay(ctx, idx, msgs); err != nil {
		t.Fatal(err)
	} else if len(paths) != 2 {
		t.Fatalf("应写入 2 条合法 caption，实际 %d", len(paths))
	}

	got, ok, err := idx.Get(ctx, "/a.txt")
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if got.MessageID != 2 || got.DocumentID != 20 || got.Size != 200 {
		t.Fatalf("latest should win: %+v", got)
	}
}

type fakeFetcher struct {
	msgs   []Message
	err    error
	minIDs []int
}

func (f *fakeFetcher) History(context.Context, int) ([]Message, error) {
	return f.msgs, f.err
}

func (f *fakeFetcher) HistorySince(_ context.Context, minID int, _ int) ([]Message, error) {
	f.minIDs = append(f.minIDs, minID)
	var out []Message
	for _, m := range f.msgs {
		if m.ID > minID {
			out = append(out, m)
		}
	}
	return out, f.err
}

func TestSync(t *testing.T) {
	idx, err := index.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	ctx := context.Background()
	f := &fakeFetcher{msgs: []Message{
		{ID: 1, Caption: `{"path":"/x.txt"}`, Size: 10},
	}}
	if err := Sync(ctx, idx, f, 10); err != nil {
		t.Fatal(err)
	}

	got, ok, err := idx.Get(ctx, "/x.txt")
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if got.Size != 10 {
		t.Fatalf("unexpected: %+v", got)
	}

	// 第二次增量：只拉 ID 大于 1 的新消息
	f.msgs = append(f.msgs, Message{ID: 2, Caption: `{"path":"/y.txt"}`, Size: 20})
	if err := Sync(ctx, idx, f, 10); err != nil {
		t.Fatal(err)
	}
	if len(f.minIDs) != 2 || f.minIDs[0] != 0 || f.minIDs[1] != 1 {
		t.Fatalf("增量游标错误: %v", f.minIDs)
	}
	if _, ok, _ := idx.Get(ctx, "/y.txt"); !ok {
		t.Fatal("增量拉取没有写入 /y.txt")
	}
}
