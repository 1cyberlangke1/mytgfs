package telegramstore

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

// TestIsFileReferenceError 只把 file_reference 系列错误判为可刷新。
func TestIsFileReferenceError(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{tgerr.New(400, "FILE_REFERENCE_EXPIRED"), true},
		{tgerr.New(400, "FILE_REFERENCE_INVALID"), true},
		{tgerr.New(400, "FILE_REFERENCE_EMPTY"), true},
		{fmt.Errorf("wrap: %w", tgerr.New(400, "FILE_REFERENCE_EXPIRED")), true},
		{tgerr.New(400, "MESSAGE_ID_INVALID"), false},
		{tgerr.New(420, "FLOOD_WAIT_30"), false},
		{errors.New("network down"), false},
		{nil, false},
	}
	for _, c := range cases {
		if got := IsFileReferenceError(c.err); got != c.want {
			t.Fatalf("IsFileReferenceError(%v)=%v，期望 %v", c.err, got, c.want)
		}
	}
}

// TestRefreshReturnsLatestReference 按消息号重新拉取 document 元数据。
func TestRefreshReturnsLatestReference(t *testing.T) {
	api := newFakeAPI()
	api.getMessagesRes = &tg.MessagesChannelMessages{Messages: []tg.MessageClass{
		&tg.Message{ID: 55, Media: &tg.MessageMediaDocument{Document: &tg.Document{
			ID: 900, AccessHash: 123, FileReference: []byte{9, 9}, Size: 42,
		}}},
	}}
	s := newTestStore(api)

	part, err := s.Refresh(context.Background(), 55)
	if err != nil {
		t.Fatal(err)
	}
	if part.MessageID != 55 || part.DocumentID != 900 || part.AccessHash != 123 || part.Size != 42 {
		t.Fatalf("Part 字段错误: %+v", part)
	}
	if string(part.FileReference) != "\x09\x09" {
		t.Fatalf("file_reference 错误: %v", part.FileReference)
	}
	if len(api.getMessages) != 1 || api.getMessages[0].ID[0].(*tg.InputMessageID).ID != 55 {
		t.Fatalf("请求消息号错误: %+v", api.getMessages)
	}
	if api.channelCalls != 1 {
		t.Fatalf("频道应只解析 1 次，实际 %d", api.channelCalls)
	}
}

// TestRefreshNoDocument 消息存在但不是 document 时报 ErrNoDocument。
func TestRefreshNoDocument(t *testing.T) {
	api := newFakeAPI()
	api.getMessagesRes = &tg.MessagesChannelMessages{Messages: []tg.MessageClass{
		&tg.Message{ID: 55, Message: "纯文本"},
	}}
	s := newTestStore(api)

	if _, err := s.Refresh(context.Background(), 55); !errors.Is(err, ErrNoDocument) {
		t.Fatalf("应返回 ErrNoDocument，实际 %v", err)
	}
}

// TestRefreshPropagatesRPCError getMessages 失败时错误带上下文返回。
func TestRefreshPropagatesRPCError(t *testing.T) {
	boom := errors.New("rpc down")
	api := newFakeAPI()
	api.getMessagesErr = boom
	s := newTestStore(api)

	if _, err := s.Refresh(context.Background(), 55); !errors.Is(err, boom) {
		t.Fatalf("错误应可 unwrap，实际 %v", err)
	}
}
