package telegramstore

// 节点消息读写单测：文本消息往返、批量取节点、文件元数据解析。

import (
	"errors"
	"testing"

	"github.com/gotd/td/tg"

	"mytgfs/internal/dirent"
)

// TestNodeTextRoundTrip 发节点消息、改名编辑、按消息号读回正文。
func TestNodeTextRoundTrip(t *testing.T) {
	ctx := t.Context()
	api := newFakeAPI()
	s := newTestStore(api)
	body, err := dirent.Encode(dirent.Node{Name: "a.txt", Parent: 0, Sha: "ab"})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	id, err := s.SendNode(ctx, body)
	if err != nil {
		t.Fatalf("SendNode: %v", err)
	}
	got, err := s.Text(ctx, id)
	if err != nil || got != body {
		t.Fatalf("Text=(%q,%v)", got, err)
	}
	edited, err := dirent.Encode(dirent.Node{Name: "b.txt", Parent: id, Dir: true})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if err := s.EditNode(ctx, id, edited); err != nil {
		t.Fatalf("EditNode: %v", err)
	}
	if got, _ := s.Text(ctx, id); got != edited {
		t.Fatalf("编辑后正文=%q", got)
	}
	if len(api.sendMedia) != 0 {
		t.Fatal("节点消息不该走 sendMedia")
	}
}

// TestTextMissing 消息不存在返回 ErrNoMessage。
func TestTextMissing(t *testing.T) {
	s := newTestStore(newFakeAPI())
	if _, err := s.Text(t.Context(), 999); !errors.Is(err, ErrNoMessage) {
		t.Fatalf("Text(999)=%v", err)
	}
}

// TestFetchNodesDirAndFile 目录节点与文件节点都能解析；聊天消息忽略。
func TestFetchNodesDirAndFile(t *testing.T) {
	ctx := t.Context()
	api := newFakeAPI()
	s := newTestStore(api)
	dirBody, _ := dirent.Encode(dirent.Node{Name: "docs", Parent: 0, Dir: true})
	fileBody, _ := dirent.Encode(dirent.Node{Name: "a.bin", Parent: 11, Sha: "cd"})
	api.getMessagesRes = &tg.MessagesChannelMessages{Messages: []tg.MessageClass{
		&tg.Message{ID: 11, Message: dirBody},
		&tg.Message{ID: 12, Message: fileBody, Media: &tg.MessageMediaDocument{Document: &tg.Document{
			ID: 900, AccessHash: 800, FileReference: []byte{1}, Size: 3,
		}}},
		&tg.Message{ID: 13, Message: "hello from chat"},
		&tg.MessageEmpty{ID: 14},
	}}
	nodes, err := s.FetchNodes(ctx, []int{11, 12, 13, 14})
	if err != nil {
		t.Fatalf("FetchNodes: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("节点数=%d %+v", len(nodes), nodes)
	}
	if !nodes[11].IsDir || nodes[11].Name != "docs" {
		t.Fatalf("目录节点=%+v", nodes[11])
	}
	if nodes[12].IsDir || nodes[12].Parent != 11 || nodes[12].Sha != "cd" || nodes[12].DocumentID != 900 || nodes[12].Size != 3 {
		t.Fatalf("文件节点=%+v", nodes[12])
	}
}

// TestFetchNodesFileWithoutDocument 文件节点缺 document 时不算节点。
func TestFetchNodesFileWithoutDocument(t *testing.T) {
	api := newFakeAPI()
	api.getMessagesRes = &tg.MessagesChannelMessages{Messages: []tg.MessageClass{
		&tg.Message{ID: 5, Message: `{"v":2,"t":"f","n":"a.bin"}`},
	}}
	nodes, err := newTestStore(api).FetchNodes(t.Context(), []int{5})
	if err != nil {
		t.Fatalf("FetchNodes: %v", err)
	}
	if len(nodes) != 0 {
		t.Fatalf("不该解析出节点: %+v", nodes)
	}
}

// TestFetchNodesEmpty 空 ids 不发 RPC。
func TestFetchNodesEmpty(t *testing.T) {
	api := newFakeAPI()
	nodes, err := newTestStore(api).FetchNodes(t.Context(), nil)
	if err != nil || len(nodes) != 0 {
		t.Fatalf("FetchNodes(nil)=(%v,%v)", nodes, err)
	}
	if len(api.getMessages) != 0 {
		t.Fatal("空 ids 不该调用 getMessages")
	}
}

// TestNodeOfRejectsLegacy 旧版 caption 一律不认。
func TestNodeOfRejectsLegacy(t *testing.T) {
	if _, ok := NodeOf(&tg.Message{ID: 1}, `{"path":"/a.txt","sha256":"x"}`); ok {
		t.Fatal("旧版 caption 不该解析成功")
	}
}
