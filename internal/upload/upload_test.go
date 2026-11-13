package upload

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"mytgfs/internal/index"
	"mytgfs/internal/telegramstore"
)

// fakeIndex 只回答一位命中结果。
type fakeIndex struct {
	hit index.File
	ok  bool
	err error
}

func (f fakeIndex) FindContent(context.Context, string, int64, string) (index.File, bool, error) {
	return f.hit, f.ok, f.err
}

// fakeStore 记录引用与上传各走了几次。
type fakeStore struct {
	copies    int
	uploads   int
	copyErr   error
	uploadErr error
}

func (f *fakeStore) Copy(_ context.Context, src *telegramstore.Part, _, _, _ string) (*telegramstore.Part, error) {
	f.copies++
	if f.copyErr != nil {
		return nil, f.copyErr
	}
	return &telegramstore.Part{MessageID: 7, DocumentID: src.DocumentID}, nil
}

func (f *fakeStore) UploadResumable(context.Context, io.ReaderAt, int64, string, string, string, string) (*telegramstore.Part, error) {
	f.uploads++
	if f.uploadErr != nil {
		return nil, f.uploadErr
	}
	return &telegramstore.Part{MessageID: 11, DocumentID: 99}, nil
}

// countingQueue 记录队列被调用几次。
type countingQueue struct{ calls int }

func (q *countingQueue) Do(ctx context.Context, _ string, fn func(context.Context) error) error {
	q.calls++
	return fn(ctx)
}

// TestHash 校验摘要与标准 sha256 一致。
func TestHash(t *testing.T) {
	got, err := Hash(strings.NewReader("abc"), 3)
	if err != nil {
		t.Fatal(err)
	}
	const want = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	if got != want {
		t.Fatalf("摘要 = %s，期望 %s", got, want)
	}
}

// TestWriteReusesExistingContent 命中同内容时只发引用消息，不占用队列。
func TestWriteReusesExistingContent(t *testing.T) {
	idx := fakeIndex{hit: index.File{Path: "/a.bin", DocumentID: 9}, ok: true}
	st := &fakeStore{}
	q := &countingQueue{}
	part, reused, err := Write(context.Background(), idx, st, Request{
		Path: "/b.bin", Reader: strings.NewReader("x"), Size: 1, SHA: "h", Queue: q,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reused || part == nil || part.DocumentID != 9 {
		t.Fatalf("应复用，得到 reused=%v part=%+v", reused, part)
	}
	if st.copies != 1 || st.uploads != 0 || q.calls != 0 {
		t.Fatalf("引用=%d 上传=%d 队列=%d，期望 1/0/0", st.copies, st.uploads, q.calls)
	}
}

// TestWriteSkipsSelfHit 同路径命中不复用，避免自己引用自己。
func TestWriteSkipsSelfHit(t *testing.T) {
	idx := fakeIndex{hit: index.File{Path: "/b.bin", DocumentID: 9}, ok: true}
	st := &fakeStore{}
	_, reused, err := Write(context.Background(), idx, st, Request{
		Path: "/b.bin", Reader: strings.NewReader("x"), Size: 1, SHA: "h",
	})
	if err != nil {
		t.Fatal(err)
	}
	if reused || st.copies != 0 || st.uploads != 1 {
		t.Fatalf("同路径应上传：reused=%v copies=%d uploads=%d", reused, st.copies, st.uploads)
	}
}

// TestQueueWrapsUpload 队列只包住真正的上传。
func TestQueueWrapsUpload(t *testing.T) {
	st := &fakeStore{}
	q := &countingQueue{}
	if _, _, err := Write(context.Background(), fakeIndex{}, st, Request{
		Path: "/c.bin", Reader: strings.NewReader("x"), Size: 1, SHA: "h", Queue: q,
	}); err != nil {
		t.Fatal(err)
	}
	if q.calls != 1 || st.uploads != 1 {
		t.Fatalf("队列=%d 上传=%d，期望 1/1", q.calls, st.uploads)
	}
}

// TestCopyFailureFallsBack 引用失败必须回退成上传而不是报错。
func TestCopyFailureFallsBack(t *testing.T) {
	idx := fakeIndex{hit: index.File{Path: "/a.bin", DocumentID: 9}, ok: true}
	st := &fakeStore{copyErr: errors.New("FILE_REFERENCE_EXPIRED")}
	part, reused, err := Write(context.Background(), idx, st, Request{
		Path: "/d.bin", Reader: strings.NewReader("x"), Size: 1, SHA: "h",
	})
	if err != nil {
		t.Fatal(err)
	}
	if reused || st.uploads != 1 || part == nil {
		t.Fatalf("应回退上传：reused=%v uploads=%d part=%+v", reused, st.uploads, part)
	}
}

// TestUploadErrorPropagates 上传失败原样上报。
func TestUploadErrorPropagates(t *testing.T) {
	boom := errors.New("FLOOD_WAIT")
	st := &fakeStore{uploadErr: boom}
	_, _, err := Write(context.Background(), fakeIndex{}, st, Request{
		Path: "/f.bin", Reader: strings.NewReader("x"), Size: 1, SHA: "h",
	})
	if !errors.Is(err, boom) {
		t.Fatalf("错误 = %v，期望原样返回", err)
	}
}

// TestIndexErrorPropagates 索引查询失败直接上报，不去上传。
func TestIndexErrorPropagates(t *testing.T) {
	boom := errors.New("db closed")
	st := &fakeStore{}
	_, _, err := Write(context.Background(), fakeIndex{err: boom}, st, Request{
		Path: "/e.bin", Reader: strings.NewReader("x"), Size: 1, SHA: "h",
	})
	if !errors.Is(err, boom) {
		t.Fatalf("错误 = %v，期望原样返回", err)
	}
	if st.uploads != 0 {
		t.Fatalf("索引失败不应上传，实际上传 %d 次", st.uploads)
	}
}
