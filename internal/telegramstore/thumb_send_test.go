package telegramstore

// 缩略图随上传发送：只接受合规 JPEG，sendMedia 要带上 Thumb。

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/gotd/td/tg"
)

// TestSendUploadedCarriesThumb 带缩略图时消息里必须有 Thumb。
func TestSendUploadedCarriesThumb(t *testing.T) {
	api := newFakeAPI()
	api.uploadSize = 10
	st := newTestStore(api)
	peer := &tg.InputPeerChannel{ChannelID: st.channelID}
	thumb := &tg.InputFile{ID: 42, Parts: 1, Name: "thumb.jpg"}
	if _, err := st.sendUploaded(context.Background(), peer,
		&UploadResume{FileID: 7, TotalParts: 1}, 10, "a.mp4", "video/mp4", "", thumb); err != nil {
		t.Fatalf("sendUploaded: %v", err)
	}
	if len(api.sendMedia) != 1 {
		t.Fatalf("sendMedia 调用次数 = %d", len(api.sendMedia))
	}
	media, ok := api.sendMedia[0].Media.(*tg.InputMediaUploadedDocument)
	if !ok {
		t.Fatalf("媒体类型 = %T", api.sendMedia[0].Media)
	}
	if media.Thumb == nil {
		t.Fatal("缩略图没有随消息发出")
	}
}

// TestSendThumbValidatesJPEG 非 JPEG / 空路径 / 读不到都当作没有缩略图。
func TestSendThumbValidatesJPEG(t *testing.T) {
	st := newTestStore(newFakeAPI())
	dir := t.TempDir()

	if got := st.sendThumb(context.Background(), ""); got != nil {
		t.Fatal("空路径不应产出缩略图")
	}
	png := filepath.Join(dir, "a.png")
	if err := os.WriteFile(png, []byte{0x89, 'P', 'N', 'G'}, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := st.sendThumb(context.Background(), png); got != nil {
		t.Fatal("PNG 不应被当成缩略图")
	}
	if got := st.sendThumb(context.Background(), filepath.Join(dir, "missing.jpg")); got != nil {
		t.Fatal("读不到的文件不应产出缩略图")
	}
	jpg := filepath.Join(dir, "a.jpg")
	if err := os.WriteFile(jpg, []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00}, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := st.sendThumb(context.Background(), jpg); got == nil {
		t.Fatal("合规 JPEG 应被上传成缩略图")
	}
}
