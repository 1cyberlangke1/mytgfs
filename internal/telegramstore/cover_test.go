package telegramstore

// 频道头像读取单测：假 API 提供 full info 与文件分片。

import (
	"errors"
	"testing"

	"github.com/gotd/td/tg"
)

// photoFixture 构造一个带头像的 full info 返回。
func photoFixture(id int64, sizes ...tg.PhotoSizeClass) *tg.Photo {
	return &tg.Photo{ID: id, AccessHash: 88, FileReference: []byte{9}, Sizes: sizes}
}

// TestCoverVersionCaches 头像 id 在 TTL 内只查一次 full info。
func TestCoverVersionCaches(t *testing.T) {
	api := newFakeAPI()
	api.fullPhoto = photoFixture(55, &tg.PhotoSize{Type: "a", W: 1, H: 1, Size: 4})
	st := New(api, 42)

	for range 2 {
		v, err := st.CoverVersion(t.Context())
		if err != nil {
			t.Fatalf("CoverVersion: %v", err)
		}
		if v != "55" {
			t.Fatalf("version = %q，期望 55", v)
		}
	}
	if api.fullCalls != 1 {
		t.Fatalf("full info 查询 %d 次，期望 1 次（缓存未生效）", api.fullCalls)
	}
}

// TestCoverVersionEmpty 频道没有头像时返回空串而不是错误。
func TestCoverVersionEmpty(t *testing.T) {
	api := newFakeAPI()
	api.fullPhoto = &tg.PhotoEmpty{}
	st := New(api, 42)
	v, err := st.CoverVersion(t.Context())
	if err != nil {
		t.Fatalf("CoverVersion: %v", err)
	}
	if v != "" {
		t.Fatalf("version = %q，期望空串", v)
	}
	if _, _, err := st.CoverImage(t.Context()); !errors.Is(err, ErrNoCover) {
		t.Fatalf("CoverImage err = %v，期望 ErrNoCover", err)
	}
}

// TestCoverImageDownloadsLargest 取最大尺寸并原样返回字节。
func TestCoverImageDownloadsLargest(t *testing.T) {
	api := newFakeAPI()
	api.fileData = []byte("01234567")
	api.fullPhoto = photoFixture(55,
		&tg.PhotoSize{Type: "small", W: 2, H: 2, Size: 2},
		&tg.PhotoSize{Type: "large", W: 4, H: 4, Size: 8},
	)
	st := New(api, 42)

	data, version, err := st.CoverImage(t.Context())
	if err != nil {
		t.Fatalf("CoverImage: %v", err)
	}
	if string(data) != "01234567" {
		t.Fatalf("data = %q", data)
	}
	if version != "55" {
		t.Fatalf("version = %q", version)
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.getFile) == 0 {
		t.Fatal("没有请求文件分片")
	}
	loc, ok := api.getFile[0].Location.(*tg.InputPhotoFileLocation)
	if !ok {
		t.Fatalf("location = %T", api.getFile[0].Location)
	}
	if loc.ThumbSize != "large" {
		t.Errorf("ThumbSize = %q，期望 large", loc.ThumbSize)
	}
}

// TestCoverChangedBaseline 首次观测只建基线，之后按 id 变化判断。
func TestCoverChangedBaseline(t *testing.T) {
	api := newFakeAPI()
	api.fullPhoto = photoFixture(55, &tg.PhotoSize{Type: "a", W: 1, H: 1, Size: 4})
	st := New(api, 42)

	if changed, err := st.CoverChanged(t.Context()); err != nil || changed {
		t.Fatalf("首次观测 changed=%v err=%v，期望 false/nil", changed, err)
	}
	if changed, err := st.CoverChanged(t.Context()); err != nil || changed {
		t.Fatalf("未变化 changed=%v err=%v，期望 false/nil", changed, err)
	}

	api.mu.Lock()
	api.fullPhoto = photoFixture(56, &tg.PhotoSize{Type: "a", W: 1, H: 1, Size: 4})
	api.mu.Unlock()
	if changed, err := st.CoverChanged(t.Context()); err != nil || !changed {
		t.Fatalf("换头像 changed=%v err=%v，期望 true/nil", changed, err)
	}

	api.mu.Lock()
	api.fullPhoto = &tg.PhotoEmpty{}
	api.mu.Unlock()
	if changed, err := st.CoverChanged(t.Context()); err != nil || !changed {
		t.Fatalf("删头像 changed=%v err=%v，期望 true/nil", changed, err)
	}
}

// TestLargestPhotoSize 跳过没有尺寸信息的条目。
func TestLargestPhotoSize(t *testing.T) {
	got, ok := largestPhotoSize([]tg.PhotoSizeClass{
		&tg.PhotoSizeEmpty{Type: "s"},
		&tg.PhotoSize{Type: "m", W: 3, H: 3, Size: 9},
		&tg.PhotoSize{Type: "l", W: 5, H: 5, Size: 25},
	})
	if !ok || got.Type != "l" {
		t.Fatalf("largest = %+v ok=%v", got, ok)
	}
	if _, ok := largestPhotoSize(nil); ok {
		t.Error("空尺寸列表不该返回 ok")
	}
}
