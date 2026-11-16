package app

// 封面路由的单测：用假读取器与假写入函数验证卷名分发与「无头像」语义。

import (
	"context"
	"errors"
	"testing"

	"github.com/gotd/td/tgerr"

	"mytgfs/internal/telegramstore"
)

// fakeCoverStore 记录读取调用并按需返回。
type fakeCoverStore struct {
	version     string
	data        []byte
	title       string
	err         error
	invalidated bool

	// setTitleErrs 按调用顺序返回；用完后按 err/title 正常处理。
	setTitleErrs []error
	titleCalls   int
	botName      string
	botErr       error
}

func (f *fakeCoverStore) CoverVersion(context.Context) (string, error) { return f.version, f.err }

// SetTitle 记录新标题，测改标题链路是否写到 store。
func (f *fakeCoverStore) SetTitle(_ context.Context, title string) error {
	f.titleCalls++
	if len(f.setTitleErrs) > 0 {
		e := f.setTitleErrs[0]
		f.setTitleErrs = f.setTitleErrs[1:]
		if e != nil {
			return e
		}
	}
	if f.err != nil {
		return f.err
	}
	f.title = title
	return nil
}

// BotUsername 回放 bot 用户名，供补授权用例断言。
func (f *fakeCoverStore) BotUsername(context.Context) (string, error) {
	return f.botName, f.botErr
}
func (f *fakeCoverStore) CoverImage(context.Context) ([]byte, string, error) {
	return f.data, f.version, f.err
}

func (f *fakeCoverStore) Title(context.Context) (string, error) { return f.title, f.err }

func (f *fakeCoverStore) InvalidateCover() { f.invalidated = true }

// newFakeProvider 构造只认 demo（频道 id 42）的封面路由。
func newFakeProvider(store *fakeCoverStore, set func(context.Context, int64, []byte) error) *CoverProvider {
	return &CoverProvider{
		lookup: func(name string) (coverVolume, bool) {
			if name == "demo" {
				return coverVolume{store: store, chatID: 42}, true
			}
			return coverVolume{}, false
		},
		set:   set,
		cache: make(map[int64]coverEntry),
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
	if !store.invalidated {
		t.Error("写头像成功后应清掉本卷封面缓存")
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

// TestCoverProviderTitle 标题按卷名分发；未挂载的卷返回空标题而不是报错。
func TestCoverProviderTitle(t *testing.T) {
	p := newFakeProvider(&fakeCoverStore{title: "我的盘"}, nil)
	got, err := p.Title(t.Context(), "demo")
	if err != nil || got != "我的盘" {
		t.Fatalf("Title = %q err=%v", got, err)
	}
	got, err = p.Title(t.Context(), "gone")
	if err != nil || got != "" {
		t.Fatalf("未挂载卷 Title = %q err=%v，期望空串无错", got, err)
	}
}

// TestSetTitleUsesBotAndSkipsGrant bot 有权限时直接改标题，不惊动 user 会话。
func TestSetTitleUsesBotAndSkipsGrant(t *testing.T) {
	store := &fakeCoverStore{}
	p := newFakeProvider(store, nil)
	p.grant = func(context.Context, int64, int64, string) error {
		t.Fatal("bot 有权限时不该补授权")
		return nil
	}
	if err := p.SetTitle(t.Context(), "demo", "新名字"); err != nil {
		t.Fatalf("SetTitle: %v", err)
	}
	if store.title != "新名字" || store.titleCalls != 1 {
		t.Fatalf("title = %q calls = %d", store.title, store.titleCalls)
	}
	if !store.invalidated {
		t.Error("改标题成功后应清频道缓存")
	}
}

// TestSetTitleGrantsThenRetries 老卷缺 ChangeInfo 时先补授权再重试一次。
//
// 输入：store 第一次返回 CHAT_ADMIN_REQUIRED，第二次成功
// 输出：grant 收到频道号/access_hash/bot 用户名，标题最终写入
// 预期：只补一次授权，重试一次即成功
func TestSetTitleGrantsThenRetries(t *testing.T) {
	store := &fakeCoverStore{
		botName:      "mytgfs_2026_bot",
		setTitleErrs: []error{tgerr.New(400, "CHAT_ADMIN_REQUIRED")},
	}
	p := newFakeProvider(store, nil)
	var gotChannel, gotAccess int64
	var gotName string
	p.grant = func(_ context.Context, channelID, accessHash int64, botUsername string) error {
		gotChannel, gotAccess, gotName = channelID, accessHash, botUsername
		return nil
	}
	if err := p.SetTitle(t.Context(), "demo", "新名字"); err != nil {
		t.Fatalf("SetTitle: %v", err)
	}
	if gotChannel != 42 || gotAccess != 0 || gotName != "mytgfs_2026_bot" {
		t.Fatalf("grant 参数 = %d/%d/%q，期望 42/0/mytgfs_2026_bot", gotChannel, gotAccess, gotName)
	}
	if store.title != "新名字" || store.titleCalls != 2 {
		t.Fatalf("title = %q calls = %d，期望补权后重试成功", store.title, store.titleCalls)
	}
}
