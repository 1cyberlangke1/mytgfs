package sync

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gotd/td/tgerr"

	"mytgfs/internal/index"
)

// fakeDiffer 按顺序吐出预置的差异结果，并记录每次请求的 pts。
type fakeDiffer struct {
	diffs []Difference
	errs  []error
	ptss  []int
	calls int
}

func (d *fakeDiffer) ChannelDifference(_ context.Context, pts int, _ int) (Difference, error) {
	d.ptss = append(d.ptss, pts)
	d.calls++
	// 先把预置错误按顺序消费完，再按顺序消费差异结果
	if len(d.errs) > 0 {
		err := d.errs[0]
		d.errs = d.errs[1:]
		return Difference{}, err
	}
	if len(d.diffs) > 0 {
		diff := d.diffs[0]
		d.diffs = d.diffs[1:]
		return diff, nil
	}
	return Difference{Pts: pts, Final: true}, nil
}

func newSyncIndex(t *testing.T) *index.Index {
	t.Helper()
	idx, err := index.OpenMemory()
	if err != nil {
		t.Fatalf("打开内存索引: %v", err)
	}
	t.Cleanup(func() { _ = idx.Close() })
	return idx
}

func msg(id int, path string, size int64) Message {
	return Message{ID: id, Caption: `{"path":"` + path + `"}`, Size: size, Date: time.Unix(int64(id), 0)}
}

// TestSyncChannelBootstrapsFromOne 首次同步从 pts=1 起跑并落盘游标。
func TestSyncChannelBootstrapsFromOne(t *testing.T) {
	idx := newSyncIndex(t)
	d := &fakeDiffer{diffs: []Difference{{
		Pts: 10, Final: true, Messages: []Message{msg(1, "/a.txt", 1), msg(2, "/b.txt", 2)},
	}}}

	res, err := SyncChannel(context.Background(), idx, d, 100)
	if err != nil {
		t.Fatal(err)
	}
	if res.Pulled != 2 || res.Pts != 10 || res.Rounds != 1 {
		t.Fatalf("统计不符: %+v", res)
	}
	if len(d.ptss) != 1 || d.ptss[0] != bootstrapPts {
		t.Fatalf("首轮 pts 应为 %d，实际 %v", bootstrapPts, d.ptss)
	}
	v, ok, err := idx.GetMeta(context.Background(), MetaChannelPts)
	if err != nil || !ok || v != 10 {
		t.Fatalf("pts 未落盘: (%d,%v,%v)", v, ok, err)
	}
}

// TestSyncChannelResumesFromMeta 已有游标时从该 pts 继续，不重头拉。
func TestSyncChannelResumesFromMeta(t *testing.T) {
	idx := newSyncIndex(t)
	if err := idx.PutMeta(context.Background(), MetaChannelPts, 33); err != nil {
		t.Fatal(err)
	}
	d := &fakeDiffer{diffs: []Difference{{Pts: 40, Final: true, Messages: []Message{msg(34, "/x.txt", 1)}}}}

	if _, err := SyncChannel(context.Background(), idx, d, 100); err != nil {
		t.Fatal(err)
	}
	if len(d.ptss) != 1 || d.ptss[0] != 33 {
		t.Fatalf("应从 33 继续，实际 %v", d.ptss)
	}
}

// TestSyncChannelFollowsNonFinal 非 final 的切片要连续拉直到追平。
func TestSyncChannelFollowsNonFinal(t *testing.T) {
	idx := newSyncIndex(t)
	d := &fakeDiffer{diffs: []Difference{
		{Pts: 5, Final: false, Messages: []Message{msg(1, "/a.txt", 1)}},
		{Pts: 9, Final: false, Messages: []Message{msg(2, "/b.txt", 1)}},
		{Pts: 12, Final: true, Messages: []Message{msg(3, "/c.txt", 1)}},
	}}

	res, err := SyncChannel(context.Background(), idx, d, 50)
	if err != nil {
		t.Fatal(err)
	}
	if res.Pulled != 3 || res.Pts != 12 || res.Rounds != 3 {
		t.Fatalf("统计不符: %+v", res)
	}
	want := []int{1, 5, 9}
	if len(d.ptss) != 3 || d.ptss[0] != want[0] || d.ptss[1] != want[1] || d.ptss[2] != want[2] {
		t.Fatalf("pts 序列错误: %v", d.ptss)
	}
}

// TestSyncChannelDeletes 删除事件要按消息号清索引。
func TestSyncChannelDeletes(t *testing.T) {
	idx := newSyncIndex(t)
	ctx := context.Background()
	if _, err := Replay(ctx, idx, []Message{msg(7, "/gone.txt", 3)}); err != nil {
		t.Fatal(err)
	}
	d := &fakeDiffer{diffs: []Difference{{Pts: 9, Final: true, Deleted: []int{7}}}}

	res, err := SyncChannel(ctx, idx, d, 100)
	if err != nil {
		t.Fatal(err)
	}
	if res.Deleted != 1 {
		t.Fatalf("删除计数错误: %+v", res)
	}
	if _, ok, _ := idx.Get(ctx, "/gone.txt"); ok {
		t.Fatal("被删除的消息应从索引移除")
	}
}

// TestSyncChannelPtsInvalidResets pts 失效时重置到起点并重试一次。
func TestSyncChannelPtsInvalidResets(t *testing.T) {
	idx := newSyncIndex(t)
	ctx := context.Background()
	if err := idx.PutMeta(ctx, MetaChannelPts, 88); err != nil {
		t.Fatal(err)
	}
	d := &fakeDiffer{
		errs:  []error{tgerr.New(400, "PERSISTENT_TIMESTAMP_EMPTY")},
		diffs: []Difference{{Pts: 90, Final: true, Messages: []Message{msg(89, "/r.txt", 1)}}},
	}

	res, err := SyncChannel(ctx, idx, d, 100)
	if err != nil {
		t.Fatal(err)
	}
	if res.Pts != 90 {
		t.Fatalf("重置后应追到 90，实际 %+v", res)
	}
	if len(d.ptss) != 2 || d.ptss[0] != 88 || d.ptss[1] != bootstrapPts {
		t.Fatalf("pts 序列应为 [88 1]，实际 %v", d.ptss)
	}
}

// TestSyncChannelTooLong 差异过大时保留最新消息并标记 Truncated。
func TestSyncChannelTooLong(t *testing.T) {
	idx := newSyncIndex(t)
	d := &fakeDiffer{diffs: []Difference{{
		Pts: 500, Final: true, TooLong: true, Messages: []Message{msg(499, "/new.txt", 1)},
	}}}

	res, err := SyncChannel(context.Background(), idx, d, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Truncated || res.Pts != 500 || res.Pulled != 1 {
		t.Fatalf("统计不符: %+v", res)
	}
	if _, ok, _ := idx.Get(context.Background(), "/new.txt"); !ok {
		t.Fatal("TooLong 返回的最新消息应写入索引")
	}
}

// TestSyncChannelPropagatesError 非 pts 失效的错误要原样上报，且不推进游标。
func TestSyncChannelPropagatesError(t *testing.T) {
	idx := newSyncIndex(t)
	ctx := context.Background()
	if err := idx.PutMeta(ctx, MetaChannelPts, 12); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("network down")
	d := &fakeDiffer{errs: []error{boom}}

	if _, err := SyncChannel(ctx, idx, d, 100); !errors.Is(err, boom) {
		t.Fatalf("错误应原样返回，实际 %v", err)
	}
	if v, _, _ := idx.GetMeta(ctx, MetaChannelPts); v != 12 {
		t.Fatalf("失败时游标不应变化，实际 %d", v)
	}
}

// TestIsPtsInvalid 只把 EMPTY/INVALID 视为需要重置。
func TestIsPtsInvalid(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{tgerr.New(400, "PERSISTENT_TIMESTAMP_EMPTY"), true},
		{tgerr.New(400, "PERSISTENT_TIMESTAMP_INVALID"), true},
		{tgerr.New(500, "PERSISTENT_TIMESTAMP_OUTDATED"), false},
		{errors.New("PERSISTENT_TIMESTAMP_INVALID"), false},
		{nil, false},
	}
	for _, c := range cases {
		if got := IsPtsInvalid(c.err); got != c.want {
			t.Fatalf("IsPtsInvalid(%v)=%v，期望 %v", c.err, got, c.want)
		}
	}
}
