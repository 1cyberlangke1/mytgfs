package telegramstore

// 视频封面的 stripped 缩略图测试：与消息免费到达，展开零网络请求。

import (
	"bytes"
	"strings"
	"testing"

	"github.com/gotd/td/telegram/thumbnail"
	"github.com/gotd/td/tg"

	"mytgfs/internal/dirent"
)

// strippedFixture 是一段可展开的 stripped 缩略图样本（首字节必须是 0x1）。
func strippedFixture() []byte {
	return append([]byte{0x01}, bytesSample...)
}

var bytesSample = []byte{
	0x36, 0x02,
	0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00,
	0x01, 0x01, 0x00, 0x00, 0x01, 0x00, 0x01, 0x00, 0x00, 0xFF, 0xD9,
}

// msgWithDoc 构造一条带 document 的频道消息，document 带指定 Thumbs。
func msgWithDoc(id int, thumbs []tg.PhotoSizeClass) *tg.Message {
	return &tg.Message{
		ID: id,
		Media: &tg.MessageMediaDocument{Document: &tg.Document{
			ID:            5000 + int64(id),
			AccessHash:    1,
			FileReference: []byte("ref"),
			Size:          1024,
			MimeType:      "video/mp4",
			Thumbs:        thumbs,
		}},
	}
}

// TestNodeOfCapturesStrippedThumb NodeOf 把 stripped 展开成 JPEG 带进 NodeInfo。
func TestNodeOfCapturesStrippedThumb(t *testing.T) {
	msg := msgWithDoc(7, []tg.PhotoSizeClass{
		&tg.PhotoStrippedSize{Type: "i", Bytes: strippedFixture()},
	})
	body, err := dirent.Encode(dirent.Node{Name: "a.mp4", Parent: 0, Sha: "aa"})
	if err != nil {
		t.Fatal(err)
	}
	info, ok := NodeOf(msg, body)
	if !ok {
		t.Fatal("NodeOf 应识别文件节点")
	}
	want, err := thumbnail.Expand(strippedFixture())
	if err != nil {
		t.Fatalf("fixture 展开失败: %v", err)
	}
	if !bytes.Equal(info.Thumb, want) {
		t.Fatalf("Thumb = %d bytes; want 展开后 %d bytes", len(info.Thumb), len(want))
	}
}

// TestThumbPrefersStrippedNoDownload 消息自带 stripped 时零下载出图。
func TestThumbPrefersStrippedNoDownload(t *testing.T) {
	api := newFakeAPI()
	api.getMessagesRes = &tg.MessagesChannelMessages{Messages: []tg.MessageClass{
		msgWithDoc(5, []tg.PhotoSizeClass{
			&tg.PhotoStrippedSize{Type: "i", Bytes: strippedFixture()},
		}),
	}}
	st := New(api, 42)
	data, err := st.Thumb(t.Context(), 5)
	if err != nil {
		t.Fatalf("Thumb: %v", err)
	}
	want, err := thumbnail.Expand(strippedFixture())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, want) {
		t.Fatalf("应返回展开后的 stripped JPEG，得到 %d bytes", len(data))
	}
	if len(api.getFile) != 0 {
		t.Fatalf("stripped 命中不应触发下载，实际 %d 次", len(api.getFile))
	}
}

// TestPartCarriesThumb 上传/复制的 Part 带上展开后的缩略图。
func TestPartCarriesThumb(t *testing.T) {
	api := newFakeAPI()
	api.uploadSize = 1024
	api.docThumbs = []tg.PhotoSizeClass{
		&tg.PhotoStrippedSize{Type: "i", Bytes: strippedFixture()},
	}
	api.getMessagesRes = &tg.MessagesChannelMessages{Messages: []tg.MessageClass{
		msgWithDoc(6, api.docThumbs),
	}}
	st := New(api, 42)
	src := strings.NewReader(strings.Repeat("x", 1024))
	part, err := st.UploadResumable(t.Context(), src, 1024, "", "v.mp4", "video/mp4", "")
	if err != nil {
		t.Fatalf("UploadResumable: %v", err)
	}
	want, err := thumbnail.Expand(strippedFixture())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(part.Thumb, want) {
		t.Fatalf("Part.Thumb 应为展开后的 JPEG，得到 %d bytes", len(part.Thumb))
	}
}
