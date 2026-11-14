package app

// 封面路由的单测：用假读取器与假写入函数验证卷名分发与「无头像」语义。

import (
	"context"
	"errors"
	"testing"

	"mytgfs/internal/telegramstore"
)

// fakeCoverStore 记录读取调用并按需返回。
type fakeCoverStore struct {
	version string
	data    []byte
	err     error
}

func (f *fakeCoverStore) CoverVersion(context.Context) (string, error) { return f.version, f.err }

func (f *fakeCoverStore) CoverImage(context.Context) ([]byte, string, error) {
	return f.data, f.version, f.err
}

// newFakeProvider 构造只认 demo（频道 id 42）的封面路由。
func newFakeProvider(store *fakeCoverStore, set func(context.Context, int64, []byte) error) *CoverProvider {
	return &CoverProvider{
		lookup: func(name string) (coverVolume, bool) {
			if name == "demo" {
				return coverVolume{store: store, chatID: 42}, true
			}
			return coverVolume{}, false
		},
		set: set,
	}
}

// TestCoverProviderRoutes 卷名命中才转发，写头像用频道 id。
func TestCoverProviderRoutes(t *testing.T) {
	store := &fakeCoverStore{version: "7", data: []byte("jpg")}
	var gotID int64
	var gotData []byte
	p := newFakeProvider(store, func(_ context.Context, id int64, data []byte) error {
		gotID, gotData = id, data
		return nil
	})
	if !p.HasVolume("demo") || p.HasVolume("nope") {
		t.Fatal("HasVolume 分发错误")
	}
	v, err := p.CoverVersion(t.Context(), "demo")
	if err != nil || v != "7" {
		t.Fatalf("CoverVersion = %q err=%v", v, err)
	}
	data, ver, err := p.CoverImage(t.Context(), "demo")
	if err != nil || string(data) != "jpg" || ver != "7" {
		t.Fatalf("CoverImage = %q %q err=%v", data, ver, err)
	}
	if err := p.SetCover(t.Context(), "demo", []byte("x")); err != nil {
		t.Fatalf("SetCover: %v", err)
	}
	if gotID != 42 || string(gotData) != "x" {
		t.Errorf("写入 channel=%d data=%q", gotID, gotData)
	}
}

// TestCoverProviderMissingVolume 未挂载的卷返回空而不是错误。
func TestCoverProviderMissingVolume(t *testing.T) {
	p := newFakeProvider(&fakeCoverStore{}, func(context.Context, int64, []byte) error {
		t.Fatal("未挂载的卷不该写头像")
		return nil
	})
	v, err := p.CoverVersion(t.Context(), "gone")
	if err != nil || v != "" {
		t.Fatalf("CoverVersion = %q err=%v", v, err)
	}
	data, ver, err := p.CoverImage(t.Context(), "gone")
	if err != nil || data != nil || ver != "" {
		t.Fatalf("CoverImage = %q %q err=%v", data, ver, err)
	}
	if err := p.SetCover(t.Context(), "gone", []byte("x")); !errors.As(err, &ErrVolumeNotFound{}) {
		t.Fatalf("SetCover err = %v，期望 ErrVolumeNotFound", err)
	}
}

// TestCoverProviderNoAvatar 频道没有头像时转成空结果。
func TestCoverProviderNoAvatar(t *testing.T) {
	store := &fakeCoverStore{err: telegramstore.ErrNoCover}
	p := newFakeProvider(store, func(context.Context, int64, []byte) error { return nil })
	data, ver, err := p.CoverImage(t.Context(), "demo")
	if err != nil || data != nil || ver != "" {
		t.Fatalf("CoverImage = %q %q err=%v", data, ver, err)
	}
}

// TestCoverProviderSetError 写入失败原样返回。
func TestCoverProviderSetError(t *testing.T) {
	boom := errors.New("CHAT_ADMIN_REQUIRED")
	p := newFakeProvider(&fakeCoverStore{}, func(context.Context, int64, []byte) error { return boom })
	if err := p.SetCover(t.Context(), "demo", []byte("x")); !errors.Is(err, boom) {
		t.Fatalf("SetCover err = %v", err)
	}
}
