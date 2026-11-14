package telegramstore

// Telegram 层单测：用假 API 代替真实网络，不挂载、不连 Telegram。
//
// 覆盖：上传（短/超长 caption、超限）、改名、删除、频道解析缓存、分块下载对齐。

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/gotd/td/tg"
)

// fakeAPI 记录所有 RPC 调用并按需返回预设结果。
//
// 并发安全：分片上传/下载会同时进入多个 goroutine，mu 保护被并发写的字段。
type fakeAPI struct {
	mu           sync.Mutex
	channelCalls int
	sendMedia    []*tg.MessagesSendMediaRequest
	sendMessage  []*tg.MessagesSendMessageRequest
	editMessage  []*tg.MessagesEditMessageRequest
	deleteMsgs   []*tg.ChannelsDeleteMessagesRequest
	getFile      []*tg.UploadGetFileRequest
	getMessages  []*tg.ChannelsGetMessagesRequest

	// texts 保存当前「频道」里的文本消息正文，供节点用例读写回放。
	texts map[int]string

	// getMessagesRes/getMessagesErr 控制 ChannelsGetMessages 的返回，供 Refresh 测试使用。
	getMessagesRes tg.MessagesMessagesClass
	getMessagesErr error

	nextID   int
	fileData []byte
	// uploadSize 是假 sendMedia 回显的 document.size，由测试在调用前设置。
	uploadSize int64
	resolveErr error
	sendErr    error
	fileErr    error
	// fileErrOnce 只让下一次 getFile 失败，用于测试 file_reference 刷新后重试。
	fileErrOnce error

	// savedParts/bigParts 记录实际发出的分片序号，供断点续传测试断言
	savedParts []int
	bigParts   []int
	// partErrOnceAt 让指定分片第一次上传失败（-1 表示不注入）
	partErrOnceAt int
	partErrOnce   error

	// 频道头像：fullPhoto 控制 chatPhoto
	fullCalls int
	fullPhoto tg.PhotoClass
	fullErr   error
}

func newFakeAPI() *fakeAPI {
	return &fakeAPI{nextID: 100, fileData: []byte("0123456789"), partErrOnceAt: -1, texts: map[int]string{}}
}

func (f *fakeAPI) ChannelsGetChannels(context.Context, []tg.InputChannelClass) (tg.MessagesChatsClass, error) {
	f.channelCalls++
	if f.resolveErr != nil {
		return nil, f.resolveErr
	}
	return &tg.MessagesChats{Chats: []tg.ChatClass{
		&tg.Channel{ID: 42, AccessHash: 777},
	}}, nil
}

// docMessage 构造一条带 document 的频道消息更新。
func (f *fakeAPI) docMessage(id int, size int64) tg.UpdatesClass {
	return &tg.Updates{Updates: []tg.UpdateClass{
		&tg.UpdateNewChannelMessage{Message: &tg.Message{
			ID: id,
			Media: &tg.MessageMediaDocument{Document: &tg.Document{
				ID:            9000 + int64(id),
				AccessHash:    5000 + int64(id),
				FileReference: []byte{1, 2, 3},
				Size:          size,
			}},
		}},
	}}
}

func (f *fakeAPI) MessagesSendMedia(_ context.Context, req *tg.MessagesSendMediaRequest) (tg.UpdatesClass, error) {
	f.sendMedia = append(f.sendMedia, req)
	if f.sendErr != nil {
		return nil, f.sendErr
	}
	f.nextID++
	return f.docMessage(f.nextID, f.uploadSize), nil
}

// docSize 已移除：文件大小由 fakeAPI.uploadSize 指定，避免猜测上传分片数。

func (f *fakeAPI) MessagesSendMessage(_ context.Context, req *tg.MessagesSendMessageRequest) (tg.UpdatesClass, error) {
	f.sendMessage = append(f.sendMessage, req)
	f.nextID++
	f.texts[f.nextID] = req.Message
	return &tg.Updates{Updates: []tg.UpdateClass{
		&tg.UpdateNewChannelMessage{Message: &tg.Message{ID: f.nextID}},
	}}, nil
}

func (f *fakeAPI) MessagesEditMessage(_ context.Context, req *tg.MessagesEditMessageRequest) (tg.UpdatesClass, error) {
	f.editMessage = append(f.editMessage, req)
	if msg, ok := req.GetMessage(); ok {
		f.texts[req.ID] = msg
	}
	return &tg.Updates{}, nil
}

func (f *fakeAPI) ChannelsDeleteMessages(_ context.Context, req *tg.ChannelsDeleteMessagesRequest) (*tg.MessagesAffectedMessages, error) {
	f.deleteMsgs = append(f.deleteMsgs, req)
	return &tg.MessagesAffectedMessages{}, nil
}

func (f *fakeAPI) ChannelsGetFullChannel(context.Context, tg.InputChannelClass) (*tg.MessagesChatFull, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fullCalls++
	if f.fullErr != nil {
		return nil, f.fullErr
	}
	return &tg.MessagesChatFull{FullChat: &tg.ChannelFull{ChatPhoto: f.fullPhoto}}, nil
}

func (f *fakeAPI) UploadGetFile(_ context.Context, req *tg.UploadGetFileRequest) (tg.UploadFileClass, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getFile = append(f.getFile, req)
	if f.fileErrOnce != nil {
		err := f.fileErrOnce
		f.fileErrOnce = nil
		return nil, err
	}
	if f.fileErr != nil {
		return nil, f.fileErr
	}
	off := int(req.Offset)
	if off >= len(f.fileData) {
		return &tg.UploadFile{Bytes: nil}, nil
	}
	end := min(off+int(req.Limit), len(f.fileData))
	return &tg.UploadFile{Bytes: f.fileData[off:end]}, nil
}

func (f *fakeAPI) UploadSaveFilePart(_ context.Context, req *tg.UploadSaveFilePartRequest) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.savedParts = append(f.savedParts, req.FilePart)
	if f.partErrOnce != nil && f.partErrOnceAt == req.FilePart {
		err := f.partErrOnce
		f.partErrOnce = nil
		return false, err
	}
	return true, nil
}

func (f *fakeAPI) UploadSaveBigFilePart(_ context.Context, req *tg.UploadSaveBigFilePartRequest) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bigParts = append(f.bigParts, req.FilePart)
	if f.partErrOnce != nil && f.partErrOnceAt == req.FilePart {
		err := f.partErrOnce
		f.partErrOnce = nil
		return false, err
	}
	return true, nil
}

// 以下四个方法由 gotd 的 downloader 接口要求，测试里不触发真实校验与 CDN。
func (f *fakeAPI) UploadGetFileHashes(context.Context, *tg.UploadGetFileHashesRequest) ([]tg.FileHash, error) {
	return nil, nil
}

func (f *fakeAPI) UploadReuploadCDNFile(context.Context, *tg.UploadReuploadCDNFileRequest) ([]tg.FileHash, error) {
	return nil, nil
}

func (f *fakeAPI) UploadGetCDNFileHashes(context.Context, *tg.UploadGetCDNFileHashesRequest) ([]tg.FileHash, error) {
	return nil, nil
}

func (f *fakeAPI) UploadGetWebFile(context.Context, *tg.UploadGetWebFileRequest) (*tg.UploadWebFile, error) {
	return &tg.UploadWebFile{}, nil
}

// ChannelsGetMessages 记录请求并返回测试预置的消息结果。
//
// 未预置结果时按 texts 里的文本消息回放，消息不存在的 id 返回 MessageEmpty，
// 与 Telegram 对已删消息的行为一致。
func (f *fakeAPI) ChannelsGetMessages(_ context.Context, req *tg.ChannelsGetMessagesRequest) (tg.MessagesMessagesClass, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getMessages = append(f.getMessages, req)
	if f.getMessagesErr != nil {
		return nil, f.getMessagesErr
	}
	if f.getMessagesRes != nil {
		return f.getMessagesRes, nil
	}
	out := make([]tg.MessageClass, 0, len(req.ID))
	for _, id := range req.ID {
		one, ok := id.(*tg.InputMessageID)
		if !ok {
			continue
		}
		if text, found := f.texts[one.ID]; found {
			out = append(out, &tg.Message{ID: one.ID, Message: text})
		} else {
			out = append(out, &tg.MessageEmpty{ID: one.ID})
		}
	}
	return &tg.MessagesChannelMessages{Messages: out}, nil
}

// newTestStore 创建关闭限速的 Store，避免单测被 1 秒间隔拖慢。
func newTestStore(api API) *Store {
	s := New(api, 42)
	s.SetInterval(0)
	return s
}

// TestUploadShortCaption 短 caption 直接随文件发送，Part 字段来自回显 document。
func TestUploadShortCaption(t *testing.T) {
	api := newFakeAPI()
	api.uploadSize = 5
	s := newTestStore(api)
	ctx := context.Background()

	part, err := s.Upload(ctx, bytes.NewReader([]byte("hello")), 5, "a.txt", "text/plain", `{"path":"/a.txt"}`)
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if len(api.sendMessage) != 0 {
		t.Fatalf("短 caption 不应发额外消息，实际 %d 条", len(api.sendMessage))
	}
	if len(api.sendMedia) != 1 {
		t.Fatalf("应发 1 条媒体消息，实际 %d", len(api.sendMedia))
	}
	if got := api.sendMedia[0].Message; got != `{"path":"/a.txt"}` {
		t.Fatalf("caption 错误: %q", got)
	}
	if part.MessageID == 0 || part.DocumentID == 0 || part.Size != 5 {
		t.Fatalf("Part 字段不完整: %+v", part)
	}
}

// TestUploadTooLarge 超过单文件上限直接拒绝，不调用任何 RPC。
func TestUploadTooLarge(t *testing.T) {
	api := newFakeAPI()
	s := newTestStore(api)

	_, err := s.Upload(context.Background(), strings.NewReader("x"), MaxFileSize+1, "big.bin", "application/octet-stream", "c")
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("应返回 ErrTooLarge，实际 %v", err)
	}
	if len(api.sendMedia) != 0 || len(api.sendMessage) != 0 {
		t.Fatal("超限不应调用发消息接口")
	}
}

// TestUploadCaptionInline 节点 caption 直接写进文件消息，不额外发元信息消息。
func TestUploadCaptionInline(t *testing.T) {
	api := newFakeAPI()
	api.uploadSize = 3
	s := newTestStore(api)
	body := `{"v":2,"t":"f","n":"a.txt","p":0,"s":"abc"}`

	_, err := s.Upload(context.Background(), bytes.NewReader([]byte("abc")), 3, "a.txt", "text/plain", body)
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if len(api.sendMessage) != 0 {
		t.Fatalf("不该发元信息消息，实际 %+v", api.sendMessage)
	}
	if len(api.sendMedia) != 1 || api.sendMedia[0].Message != body {
		t.Fatalf("caption 应原样写入，实际 %+v", api.sendMedia)
	}
}

// TestRename 改名只修改 caption，并带上正确的 peer 与消息号。
func TestRename(t *testing.T) {
	api := newFakeAPI()
	s := newTestStore(api)

	if err := s.Rename(context.Background(), 55, `{"path":"/b.txt"}`); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if len(api.editMessage) != 1 {
		t.Fatalf("应调用 1 次 editMessage，实际 %d", len(api.editMessage))
	}
	req := api.editMessage[0]
	if req.ID != 55 || req.Message != `{"path":"/b.txt"}` {
		t.Fatalf("editMessage 参数错误: id=%d msg=%q", req.ID, req.Message)
	}
	if peer, ok := req.Peer.(*tg.InputPeerChannel); !ok || peer.ChannelID != 42 {
		t.Fatalf("peer 错误: %+v", req.Peer)
	}
}

// TestDelete 删除消息：空列表不发请求，非空按 ID 删除。
func TestDelete(t *testing.T) {
	api := newFakeAPI()
	s := newTestStore(api)
	ctx := context.Background()

	if err := s.Delete(ctx); err != nil {
		t.Fatalf("空删除应成功: %v", err)
	}
	if len(api.deleteMsgs) != 0 {
		t.Fatal("空删除不应发请求")
	}
	if err := s.Delete(ctx, 7, 8); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(api.deleteMsgs) != 1 {
		t.Fatalf("应调用 1 次删除，实际 %d", len(api.deleteMsgs))
	}
	got := api.deleteMsgs[0]
	if len(got.ID) != 2 || got.ID[0] != 7 || got.ID[1] != 8 {
		t.Fatalf("删除 ID 错误: %v", got.ID)
	}
}

// TestResolveChannelCached 频道只解析一次，后续复用 access_hash。
func TestResolveChannelCached(t *testing.T) {
	api := newFakeAPI()
	s := newTestStore(api)
	ctx := context.Background()

	if err := s.Delete(ctx, 1); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := s.Delete(ctx, 2); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if api.channelCalls != 1 {
		t.Fatalf("频道应只解析 1 次，实际 %d", api.channelCalls)
	}
}

// TestReadRangeAlignsToPart 读取按 partSize 对齐，内容与偏移一致。
func TestReadRangeAlignsToPart(t *testing.T) {
	api := newFakeAPI()
	s := newTestStore(api)
	api.fileData = make([]byte, 5000)
	for i := range api.fileData {
		api.fileData[i] = byte(i % 251)
	}
	p := &Part{DocumentID: 1, AccessHash: 2, FileReference: []byte{9}, Size: int64(len(api.fileData))}

	data, err := s.ReadRange(context.Background(), p, 1500, 1200, nil)
	if err != nil {
		t.Fatalf("ReadRange: %v", err)
	}
	if !bytes.Equal(data, api.fileData[1500:2700]) {
		t.Fatal("读取内容与偏移不一致")
	}
	if len(api.getFile) == 0 {
		t.Fatal("没有发出 getFile 请求")
	}
	for _, req := range api.getFile {
		if req.Offset%partSize != 0 {
			t.Fatalf("offset 未按 partSize 对齐: %d", req.Offset)
		}
		if req.Limit > partSize || req.Limit%4096 != 0 {
			t.Fatalf("limit 非法: %d", req.Limit)
		}
	}
}

// TestReadRangeOutOfBounds 越界读取直接拒绝，不发请求。
func TestReadRangeOutOfBounds(t *testing.T) {
	api := newFakeAPI()
	s := newTestStore(api)
	p := &Part{Size: 10}

	if _, err := s.ReadRange(context.Background(), p, 5, 10, nil); !errors.Is(err, ErrBadRange) {
		t.Fatalf("应返回 ErrBadRange，实际 %v", err)
	}
	if len(api.getFile) != 0 {
		t.Fatal("越界不应发请求")
	}
}

// itoa 已移除，测试直接比较字面量。
