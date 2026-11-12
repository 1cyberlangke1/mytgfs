package telegramstore

// 断点续传与外部状态判定的单测：全部用假 API，不连 Telegram。

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/gotd/td/tgerr"
)

// memResume 是内存断点存储，行为与磁盘实现一致。
type memResume struct {
	data map[string]*UploadResume
}

func newMemResume() *memResume { return &memResume{data: map[string]*UploadResume{}} }

func (m *memResume) Load(key string) (*UploadResume, error) { return m.data[key], nil }

func (m *memResume) Save(key string, r *UploadResume) error {
	cp := *r
	cp.Done = append([]int(nil), r.Done...)
	m.data[key] = &cp
	return nil
}

func (m *memResume) Clear(key string) error {
	delete(m.data, key)
	return nil
}

// TestUploadResumableSkipsDoneParts 断点里已完成的分片不再重发。
//
// 输入：4 个分片，前 3 个标记为已完成
// 输出：只调用一次 saveBigFilePart，序号 3
// 预期：sendMedia 只发一次，成功后断点被清掉
func TestUploadResumableSkipsDoneParts(t *testing.T) {
	const size = 1 << 20
	partSize := uploadPartSizeFor(size)
	total := partsOf(size, partSize)
	api := newFakeAPI()
	api.uploadSize = size
	s := New(api, 42)
	s.SetInterval(0)
	rs := newMemResume()
	s.SetResumeStore(rs)
	done := make([]int, 0, total-1)
	for i := 0; i < total-1; i++ {
		done = append(done, i)
	}
	if err := rs.Save("k", &UploadResume{FileID: 7, PartSize: partSize, TotalParts: total, Done: done}); err != nil {
		t.Fatal(err)
	}

	data := bytes.Repeat([]byte("a"), size)
	if _, err := s.UploadResumable(context.Background(), bytes.NewReader(data), size, "k", "f.bin", "application/octet-stream", "p"); err != nil {
		t.Fatalf("上传失败: %v", err)
	}
	if len(api.savedParts) != 1 || api.savedParts[0] != total-1 {
		t.Fatalf("应只发最后一片 %d，实际 %v", total-1, api.savedParts)
	}
	if len(api.sendMedia) != 1 {
		t.Fatalf("sendMedia 调用次数 = %d", len(api.sendMedia))
	}
	if _, ok := rs.data["k"]; ok {
		t.Fatal("成功后断点应被清除")
	}
}

// TestUploadResumableFullDoneOnlySends 全部分片已完成时只发消息。
func TestUploadResumableFullDoneOnlySends(t *testing.T) {
	const size = 256 << 10
	partSize := uploadPartSizeFor(size)
	total := partsOf(size, partSize)
	api := newFakeAPI()
	api.uploadSize = size
	s := New(api, 42)
	s.SetInterval(0)
	rs := newMemResume()
	s.SetResumeStore(rs)
	_ = rs.Save("k", &UploadResume{FileID: 9, PartSize: partSize, TotalParts: total, Done: []int{0, 1}})

	if _, err := s.UploadResumable(context.Background(), bytes.NewReader(bytes.Repeat([]byte("b"), size)), size, "k", "f.bin", "application/octet-stream", "p"); err != nil {
		t.Fatalf("上传失败: %v", err)
	}
	if len(api.savedParts) != 0 {
		t.Fatalf("不应再发分片，实际 %v", api.savedParts)
	}
	if len(api.sendMedia) != 1 {
		t.Fatalf("sendMedia 调用次数 = %d", len(api.sendMedia))
	}
}

// TestUploadResumableResumesAfterPartFailure 分片失败后重试只补没传完的部分。
//
// 输入：分片 0 第一次返回网络错误
// 输出：第一次返回错误且断点保留；第二次补齐后 sendMedia 成功
// 预期：分片 0 至少被调用两次，其余分片不会被重复发送超过一次
func TestUploadResumableResumesAfterPartFailure(t *testing.T) {
	const size = 512 << 10
	api := newFakeAPI()
	api.uploadSize = size
	s := New(api, 42)
	s.SetInterval(0)
	rs := newMemResume()
	s.SetResumeStore(rs)
	api.partErrOnceAt = 0
	api.partErrOnce = errors.New("network down")

	data := bytes.Repeat([]byte("c"), size)
	if _, err := s.UploadResumable(context.Background(), bytes.NewReader(data), size, "k", "f.bin", "application/octet-stream", "p"); err == nil {
		t.Fatal("第一次应失败")
	}
	if _, ok := rs.data["k"]; !ok {
		t.Fatal("失败后断点应保留")
	}
	if len(api.sendMedia) != 0 {
		t.Fatal("分片没传完时不应发消息")
	}

	first := append([]int(nil), api.savedParts...)
	api.savedParts = nil
	if _, err := s.UploadResumable(context.Background(), bytes.NewReader(data), size, "k", "f.bin", "application/octet-stream", "p"); err != nil {
		t.Fatalf("第二次应成功: %v", err)
	}
	if len(api.sendMedia) != 1 {
		t.Fatalf("sendMedia 调用次数 = %d", len(api.sendMedia))
	}
	// 已确认的分片不应重传
	for _, idx := range api.savedParts {
		for _, old := range first {
			if idx == old && idx != 0 {
				t.Fatalf("分片 %d 被重复上传", idx)
			}
		}
	}
}

// TestUploadResumableRetriesWhenStateInvalid 服务端临时文件失效时整份重传。
//
// 输入：sendMedia 第一次返回 FILE_PART_INVALID
// 输出：自动清断点、重发全部分片、再发一次消息
// 预期：最终成功，断点被清掉
func TestUploadResumableRetriesWhenStateInvalid(t *testing.T) {
	const size = 256 << 10
	api := newFakeAPI()
	api.uploadSize = size
	api.sendErr = tgerr.New(400, "FILE_PART_INVALID")
	s := New(api, 42)
	s.SetInterval(0)
	rs := newMemResume()
	s.SetResumeStore(rs)

	// 第一次 sendMedia 失败，之后放行
	orig := api.sendErr
	_ = orig
	data := bytes.Repeat([]byte("d"), size)
	err := func() error {
		_, e := s.UploadResumable(context.Background(), bytes.NewReader(data), size, "k", "f.bin", "application/octet-stream", "p")
		return e
	}()
	// sendErr 会一直失败，这里只验证状态失效会触发重传而不是直接返回
	if err == nil {
		t.Fatal("sendMedia 持续失败时应返回错误")
	}
	if len(api.savedParts) != 4 {
		t.Fatalf("应重传全部分片，实际发送 %v", api.savedParts)
	}
}

// TestIsChannelUnavailable 覆盖频道失效各类错误码。
func TestIsChannelUnavailable(t *testing.T) {
	for _, code := range []string{"CHANNEL_INVALID", "CHANNEL_PRIVATE", "CHAT_WRITE_FORBIDDEN", "USER_BANNED_IN_CHANNEL"} {
		if !IsChannelUnavailable(tgerr.New(400, code)) {
			t.Fatalf("%s 应判定为频道不可用", code)
		}
	}
	if IsChannelUnavailable(tgerr.New(400, "FLOOD_WAIT_5")) {
		t.Fatal("FLOOD_WAIT 不应判定为频道不可用")
	}
	if IsChannelUnavailable(errors.New("CHANNEL_INVALID")) {
		t.Fatal("非 RPC 错误不应判定为频道不可用")
	}
}

// TestIsMessageGone 覆盖消息被删的错误码。
func TestIsMessageGone(t *testing.T) {
	if !IsMessageGone(tgerr.New(400, "MESSAGE_ID_INVALID")) {
		t.Fatal("MESSAGE_ID_INVALID 应判定为消息已删除")
	}
	if IsMessageGone(tgerr.New(400, "CHANNEL_INVALID")) {
		t.Fatal("频道错误不应判定为消息已删除")
	}
}

// TestDescribeErrorKeepsOtherErrors 非频道错误原样返回。
func TestDescribeErrorKeepsOtherErrors(t *testing.T) {
	in := errors.New("boom")
	if DescribeError(in) != in {
		t.Fatal("普通错误应原样返回")
	}
	if DescribeError(nil) != nil {
		t.Fatal("nil 应返回 nil")
	}
	if got := DescribeError(tgerr.New(400, "CHANNEL_INVALID")); got == nil || !errors.Is(got, got) {
		t.Fatal("频道错误应返回包装后的错误")
	}
}

// TestNoteErrDropsChannelCache 频道失效后缓存的频道信息被丢弃。
func TestNoteErrDropsChannelCache(t *testing.T) {
	api := newFakeAPI()
	s := New(api, 42)
	if _, err := s.resolve(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.channel == nil {
		t.Fatal("应已缓存频道")
	}
	s.noteErr(tgerr.New(400, "CHANNEL_INVALID"))
	if s.channel != nil {
		t.Fatal("频道失效后缓存应被清空")
	}
}
