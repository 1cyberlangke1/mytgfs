package app

// 视频封面零网络链路的单测：索引里的 stripped 展开字节优先于回源。

import (
	"context"
	"errors"
	"os"
	"testing"

	"mytgfs/internal/index"
	"mytgfs/internal/telegramstore"

	"github.com/gotd/td/tg"
)

func bytesEqual(a, b []byte) bool { return string(a) == string(b) }

// thumbFakeAPI 只实现缩略图链路用到的 RPC：ChannelsGetMessages 回放一条
// 带 stripped 缩略图的文档消息；其余方法不应被调用。
type thumbFakeAPI struct {
	stripped []byte
}

func (f *thumbFakeAPI) ChannelsGetChannels(context.Context, []tg.InputChannelClass) (tg.MessagesChatsClass, error) {
	return &tg.MessagesChats{Chats: []tg.ChatClass{&tg.Channel{ID: 42, AccessHash: 1}}}, nil
}

// ChannelsEditTitle 满足 telegramstore.API；本测试不改标题。
func (f *thumbFakeAPI) ChannelsEditTitle(context.Context, *tg.ChannelsEditTitleRequest) (tg.UpdatesClass, error) {
	return &tg.Updates{}, nil
}

func (f *thumbFakeAPI) ChannelsGetMessages(_ context.Context, _ *tg.ChannelsGetMessagesRequest) (tg.MessagesMessagesClass, error) {
	return &tg.MessagesChannelMessages{Messages: []tg.MessageClass{
		&tg.Message{ID: 7, Media: &tg.MessageMediaDocument{Document: &tg.Document{
			ID:         5007,
			AccessHash: 2,
			Size:       1024,
			MimeType:   "video/mp4",
			Thumbs:     []tg.PhotoSizeClass{&tg.PhotoStrippedSize{Type: "i", Bytes: f.stripped}},
		}}},
	}}, nil
}

func (f *thumbFakeAPI) ChannelsGetFullChannel(context.Context, tg.InputChannelClass) (*tg.MessagesChatFull, error) {
	return nil, errors.New("unexpected")
}

func (f *thumbFakeAPI) MessagesSendMedia(context.Context, *tg.MessagesSendMediaRequest) (tg.UpdatesClass, error) {
	return nil, errors.New("unexpected")
}

func (f *thumbFakeAPI) MessagesSendMessage(context.Context, *tg.MessagesSendMessageRequest) (tg.UpdatesClass, error) {
	return nil, errors.New("unexpected")
}

func (f *thumbFakeAPI) MessagesEditMessage(context.Context, *tg.MessagesEditMessageRequest) (tg.UpdatesClass, error) {
	return nil, errors.New("unexpected")
}

func (f *thumbFakeAPI) UploadGetFile(context.Context, *tg.UploadGetFileRequest) (tg.UploadFileClass, error) {
	return nil, errors.New("unexpected download")
}

func (f *thumbFakeAPI) UploadGetFileHashes(context.Context, *tg.UploadGetFileHashesRequest) ([]tg.FileHash, error) {
	return nil, errors.New("unexpected")
}

func (f *thumbFakeAPI) UploadReuploadCDNFile(context.Context, *tg.UploadReuploadCDNFileRequest) ([]tg.FileHash, error) {
	return nil, errors.New("unexpected")
}

func (f *thumbFakeAPI) UploadGetCDNFileHashes(context.Context, *tg.UploadGetCDNFileHashesRequest) ([]tg.FileHash, error) {
	return nil, errors.New("unexpected")
}

func (f *thumbFakeAPI) UploadGetWebFile(context.Context, *tg.UploadGetWebFileRequest) (*tg.UploadWebFile, error) {
	return nil, errors.New("unexpected")
}

func (f *thumbFakeAPI) UploadSaveFilePart(context.Context, *tg.UploadSaveFilePartRequest) (bool, error) {
	return false, errors.New("unexpected")
}

func (f *thumbFakeAPI) UploadSaveBigFilePart(context.Context, *tg.UploadSaveBigFilePartRequest) (bool, error) {
	return false, errors.New("unexpected")
}

// TestVideoThumbServesIndexThumb 索引里有封面时直接返回，不回源。
//
// 回归背景：视频封面此前每次都要拉消息+下载缩略图两个 Telegram 往返；
// 现在同步/上传时把消息自带的 stripped 缩略图展开入库，展示零请求。
func TestVideoThumbServesIndexThumb(t *testing.T) {
	idx, err := index.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = idx.Close() })
	thumb := []byte{0xFF, 0xD8, 0x01}
	if err := idx.PutNode(t.Context(), index.Node{ID: 5, Name: "v.mp4", Thumb: thumb}); err != nil {
		t.Fatal(err)
	}

	v := &VolumeFS{Index: idx, Store: telegramstore.New(nil, 42)}
	data, version, err := v.VideoThumb(t.Context(), "/v.mp4")
	if err != nil {
		t.Fatalf("VideoThumb: %v", err)
	}
	if !bytesEqual(data, thumb) {
		t.Fatalf("应返回索引里的封面字节")
	}
	if version != "5" {
		t.Fatalf("version = %q; want 消息号 5", version)
	}
}

// TestVideoThumbPersistsFetchedThumb 回源成功后写回索引，下次零请求。
func TestVideoThumbPersistsFetchedThumb(t *testing.T) {
	idx, err := index.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = idx.Close() })
	if err := idx.PutNode(t.Context(), index.Node{ID: 7, Name: "v.mp4"}); err != nil {
		t.Fatal(err)
	}

	api := &thumbFakeAPI{stripped: []byte{0x01, 0xFF, 0xD8, 0x09}}
	v := &VolumeFS{Index: idx, Store: telegramstore.New(api, 42)}

	data, _, err := v.VideoThumb(t.Context(), "/v.mp4")
	if err != nil {
		t.Fatalf("首次回源: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("回源应有字节")
	}
	n, ok, err := idx.NodeByPath(t.Context(), "/v.mp4")
	if err != nil || !ok {
		t.Fatalf("NodeByPath: ok=%v err=%v", ok, err)
	}
	if len(n.Thumb) == 0 {
		t.Fatal("回源结果应写回索引")
	}

	// 第二次：直接命中索引，Store 连接异常也不会被触达
	failStore := telegramstore.New(&thumbFakeAPI{stripped: []byte{0x01}}, 42)
	v2 := &VolumeFS{Index: idx, Store: failStore}
	again, _, err := v2.VideoThumb(t.Context(), "/v.mp4")
	if err != nil {
		t.Fatalf("第二次应零请求: %v", err)
	}
	if len(again) == 0 {
		t.Fatal("第二次应返回索引字节")
	}
}

// TestVideoThumbMissingNode 目录/不存在的路径返回 os.ErrNotExist。
func TestVideoThumbMissingNode(t *testing.T) {
	idx, err := index.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = idx.Close() })
	v := &VolumeFS{Index: idx, Store: telegramstore.New(nil, 42)}
	if _, _, err := v.VideoThumb(t.Context(), "/nope"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v; want os.ErrNotExist", err)
	}
}
