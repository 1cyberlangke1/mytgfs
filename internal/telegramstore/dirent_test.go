package telegramstore

// 目录项远端读写单测：假 API 回放文本消息，不连网。

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gotd/td/tg"

	"mytgfs/internal/dirent"
)

// entries 造 n 个文件目录项，名字长度可控。
func entries(n, nameLen int) []dirent.Entry {
	out := make([]dirent.Entry, 0, n)
	for i := 1; i <= n; i++ {
		out = append(out, dirent.Entry{
			Name:  strings.Repeat("n", nameLen) + itoa(i),
			MsgID: 1000 + i,
		})
	}
	return out
}

// itoa 避免为几个数字引入 strconv。
func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var b []byte
	for v > 0 {
		b = append([]byte{byte('0' + v%10)}, b...)
		v /= 10
	}
	return string(b)
}

// startStore 建一个限速关闭的 Store。
func startStore(t *testing.T) (*Store, *fakeAPI, context.Context) {
	t.Helper()
	api := newFakeAPI()
	return newTestStore(api), api, context.Background()
}

// Test 文本消息往返与缺失消息。
func TestTextRoundTrip(t *testing.T) {
	s, _, ctx := startStore(t)
	id, err := s.SendText(ctx, "hello")
	if err != nil {
		t.Fatalf("SendText: %v", err)
	}
	got, err := s.Text(ctx, id)
	if err != nil || got != "hello" {
		t.Fatalf("Text=(%q,%v)", got, err)
	}
	if _, err := s.Text(ctx, id+999); !errors.Is(err, ErrNoMessage) {
		t.Fatalf("缺失消息应返回 ErrNoMessage，实际 %v", err)
	}
}

// Test 新建目录 → 写少量目录项 → 读回，正文内联且无续块。
func TestUpdateDirSmall(t *testing.T) {
	s, api, ctx := startStore(t)
	head, err := s.CreateDir(ctx)
	if err != nil {
		t.Fatalf("CreateDir: %v", err)
	}
	want := entries(2, 3)
	got, err := s.UpdateDir(ctx, head, DirContent{}, want)
	if err != nil {
		t.Fatalf("UpdateDir: %v", err)
	}
	if len(got.Chunks) != 0 || got.RefMsg != 0 {
		t.Fatalf("小目录不应有续块/替身: %+v", got)
	}
	if body := api.texts[head]; !strings.Contains(body, `"t":"d"`) {
		t.Fatalf("目录消息正文=%q", body)
	}
	read, err := s.ReadDir(ctx, head)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(read.Entries) != len(want) {
		t.Fatalf("读回 %d 项，期望 %d 项", len(read.Entries), len(want))
	}
	for i := range want {
		if !dirent.Equal(read.Entries[i], want[i]) {
			t.Fatalf("第 %d 项=%+v 期望 %+v", i, read.Entries[i], want[i])
		}
	}
}

// Test 目录项多到超过单条正文预算时切续块，缩小后旧续块被删除。
func TestUpdateDirChunks(t *testing.T) {
	s, api, ctx := startStore(t)
	head, _ := s.CreateDir(ctx)
	want := entries(120, 40)

	got, err := s.UpdateDir(ctx, head, DirContent{}, want)
	if err != nil {
		t.Fatalf("UpdateDir: %v", err)
	}
	if len(got.Chunks) < 2 {
		t.Fatalf("应切出至少 2 条续块，实际 %d", len(got.Chunks))
	}
	read, err := s.ReadDir(ctx, head)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(read.Entries) != len(want) {
		t.Fatalf("读回 %d 项，期望 %d 项", len(read.Entries), len(want))
	}
	for i := range want {
		if !dirent.Equal(read.Entries[i], want[i]) {
			t.Fatalf("第 %d 项=%+v 期望 %+v", i, read.Entries[i], want[i])
		}
	}
	if len(read.Chunks) != len(got.Chunks) {
		t.Fatalf("ReadDir 续块 %d 条，写入 %d 条", len(read.Chunks), len(got.Chunks))
	}

	small := entries(1, 3)
	after, err := s.UpdateDir(ctx, head, read, small)
	if err != nil {
		t.Fatalf("UpdateDir shrink: %v", err)
	}
	if len(after.Chunks) != 0 {
		t.Fatalf("缩小后不应保留续块: %+v", after.Chunks)
	}
	if len(api.deleteMsgs) == 0 {
		t.Fatal("多余续块应被删除")
	}
	var deleted []int
	for _, req := range api.deleteMsgs {
		deleted = append(deleted, req.ID...)
	}
	for _, id := range read.Chunks {
		found := false
		for _, d := range deleted {
			if d == id {
				found = true
			}
		}
		if !found {
			t.Fatalf("续块 %d 未被删除（已删 %v）", id, deleted)
		}
	}
	again, err := s.ReadDir(ctx, head)
	if err != nil || len(again.Entries) != 1 {
		t.Fatalf("缩小后 ReadDir=(%+v,%v)", again, err)
	}
}

// Test 正文超过 900 字节时走替身消息；缩小后替身消息被删。
func TestUpdateDirRef(t *testing.T) {
	s, api, ctx := startStore(t)
	head, _ := s.CreateDir(ctx)
	want := entries(20, 30)

	got, err := s.UpdateDir(ctx, head, DirContent{}, want)
	if err != nil {
		t.Fatalf("UpdateDir: %v", err)
	}
	if got.RefMsg == 0 {
		t.Fatalf("正文过长应使用替身消息，实际 %+v", got)
	}
	if body := api.texts[head]; !strings.Contains(body, `"ref"`) {
		t.Fatalf("目录消息应为替身指针，实际 %q", body)
	}
	read, err := s.ReadDir(ctx, head)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(read.Entries) != len(want) || read.RefMsg != got.RefMsg {
		t.Fatalf("读回=%+v 期望 %d 项 ref=%d", read, len(want), got.RefMsg)
	}

	after, err := s.UpdateDir(ctx, head, read, entries(1, 3))
	if err != nil {
		t.Fatalf("UpdateDir shrink: %v", err)
	}
	if after.RefMsg != 0 {
		t.Fatalf("缩小后不应保留替身消息: %+v", after)
	}
	var deleted []int
	for _, req := range api.deleteMsgs {
		deleted = append(deleted, req.ID...)
	}
	found := false
	for _, d := range deleted {
		if d == got.RefMsg {
			found = true
		}
	}
	if !found {
		t.Fatalf("旧替身 %d 未被删除（已删 %v）", got.RefMsg, deleted)
	}
}

// Test 根目录锚点：读置顶、静默置顶。
func TestPinnedRoot(t *testing.T) {
	s, api, ctx := startStore(t)
	id, err := s.PinnedRoot(ctx)
	if err != nil || id != 0 {
		t.Fatalf("初始置顶=(%d,%v)", id, err)
	}
	if err := s.PinRoot(ctx, 55); err != nil {
		t.Fatalf("PinRoot: %v", err)
	}
	if id, err = s.PinnedRoot(ctx); err != nil || id != 55 {
		t.Fatalf("置顶后=(%d,%v)", id, err)
	}
	if api.pinCalls != 1 {
		t.Fatalf("置顶调用 %d 次", api.pinCalls)
	}
}

// Test 非目录正文读取报错。
func TestReadDirRejectsNonDir(t *testing.T) {
	s, _, ctx := startStore(t)
	id, _ := s.SendText(ctx, "hello")
	if _, err := s.ReadDir(ctx, id); err == nil {
		t.Fatal("非目录消息应报错")
	}
}

// Test 续块链读到一半断了要报错，不能静默返回半份目录。
func TestReadDirBrokenChain(t *testing.T) {
	s, api, ctx := startStore(t)
	head, _ := s.CreateDir(ctx)
	chunk, _ := s.SendText(ctx, "{}")
	body, _ := dirent.EncodeDir([]dirent.Entry{{Name: "a", MsgID: 5}}, chunk)
	_ = s.EditText(ctx, head, body)
	delete(api.texts, chunk)

	if _, err := s.ReadDir(ctx, head); err == nil {
		t.Fatal("续块缺失应报错")
	}
}

// Test 目录消息正文是文件格式时报错（防止把文件消息当目录）。
func TestReadDirFileBody(t *testing.T) {
	s, _, ctx := startStore(t)
	body, _ := dirent.EncodeFile("abc")
	id, _ := s.SendText(ctx, body)
	if _, err := s.ReadDir(ctx, id); err == nil {
		t.Fatal("文件正文不应被当作目录")
	}
}

// 断言 *tg.Message 命中文本读取路径（编译期确认类型可用）。
var _ tg.MessageClass = &tg.Message{}
