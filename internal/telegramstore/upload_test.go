package telegramstore

import (
	"context"
	"errors"
	"testing"

	"github.com/gotd/td/tg"
)

func TestUploadRejectsTooLarge(t *testing.T) {
	s := New(nil, 1)
	_, err := s.Upload(context.Background(), nil, MaxFileSize+1, "x", "application/octet-stream", "")
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("want ErrTooLarge, got %v", err)
	}
}

func TestUploadRejectsNegative(t *testing.T) {
	s := New(nil, 1)
	_, err := s.Upload(context.Background(), nil, -1, "x", "application/octet-stream", "")
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("want ErrTooLarge, got %v", err)
	}
}

func TestExtractDocumentChannelMessage(t *testing.T) {
	doc := &tg.Document{ID: 1, AccessHash: 2, FileReference: []byte{3}, Size: 4}
	msg := &tg.Message{ID: 10, Media: &tg.MessageMediaDocument{Document: doc}}
	upd := &tg.Updates{Updates: []tg.UpdateClass{
		&tg.UpdateNewChannelMessage{Message: msg},
	}}

	got, id, err := extractDocument(upd)
	if err != nil {
		t.Fatal(err)
	}
	if id != 10 || got.ID != 1 || got.Size != 4 {
		t.Fatalf("unexpected: id=%d doc=%+v", id, got)
	}
}

func TestExtractDocumentShortMessage(t *testing.T) {
	doc := &tg.Document{ID: 7, AccessHash: 8, Size: 9}
	upd := &tg.UpdateShortSentMessage{
		ID:    11,
		Media: &tg.MessageMediaDocument{Document: doc},
	}

	got, id, err := extractDocument(upd)
	if err != nil {
		t.Fatal(err)
	}
	if id != 11 || got.ID != 7 || got.Size != 9 {
		t.Fatalf("unexpected: id=%d doc=%+v", id, got)
	}
}

func TestExtractDocumentMissing(t *testing.T) {
	upd := &tg.Updates{Updates: []tg.UpdateClass{
		&tg.UpdateNewChannelMessage{Message: &tg.Message{ID: 1}},
	}}
	_, _, err := extractDocument(upd)
	if !errors.Is(err, ErrNoDocument) {
		t.Fatalf("want ErrNoDocument, got %v", err)
	}
}
