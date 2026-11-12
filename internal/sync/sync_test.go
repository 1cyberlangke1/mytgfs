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
	if err := Replay(ctx, idx, msgs); err != nil {
		t.Fatal(err)
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
	msgs []Message
	err  error
}

func (f fakeFetcher) History(context.Context, int) ([]Message, error) {
	return f.msgs, f.err
}

func TestSync(t *testing.T) {
	idx, err := index.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	ctx := context.Background()
	f := fakeFetcher{msgs: []Message{
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
}
