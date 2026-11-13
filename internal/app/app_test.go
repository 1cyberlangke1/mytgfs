package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mytgfs/internal/config"
)

// TestRunRejectsUnknownVolume 未知卷名必须在连 Telegram 之前就失败。
func TestRunRejectsUnknownVolume(t *testing.T) {
	a := &App{cfg: config.Config{Telegram: config.Telegram{ChannelID: 1}}}
	err := a.Run(context.Background(), "nope", func(context.Context, *Session) error {
		t.Fatal("不应进入回调")
		return nil
	})
	var nf ErrVolumeNotFound
	if !errors.As(err, &nf) || nf.Name != "nope" {
		t.Fatalf("错误 = %v，期望卷不存在", err)
	}
}

// TestRunRejectsMissingAPIHash 缺 api_hash 时直接返回哨兵错误。
func TestRunRejectsMissingAPIHash(t *testing.T) {
	a := &App{cfg: config.Config{Volumes: []config.Volume{{Name: "demo", ChatID: 1}}}}
	err := a.Run(context.Background(), "demo", func(context.Context, *Session) error {
		t.Fatal("不应进入回调")
		return nil
	})
	if !errors.Is(err, ErrMissingAPIHash) {
		t.Fatalf("错误 = %v，期望缺 api_hash", err)
	}
}

// TestOpenVolumeRejectsUnknownVolume 未知卷名同样在开索引前失败。
func TestOpenVolumeRejectsUnknownVolume(t *testing.T) {
	a := &App{cfg: config.Config{Telegram: config.Telegram{ChannelID: 1}}}
	if _, err := a.OpenVolume(context.Background(), "nope", nil); err == nil {
		t.Fatal("期望返回卷不存在")
	}
}

// TestIndexCreatesPerVolumeDB 索引按卷名分文件，且目录自动创建。
func TestIndexCreatesPerVolumeDB(t *testing.T) {
	dir := t.TempDir()
	old, werr := os.Getwd()
	if werr != nil {
		t.Fatal(werr)
	}
	if cerr := os.Chdir(dir); cerr != nil {
		t.Fatal(cerr)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })

	a := &App{}
	idx, err := a.Index("demo")
	if err != nil {
		t.Fatal(err)
	}
	if err := idx.Close(); err != nil {
		t.Fatal(err)
	}
	if _, serr := os.Stat(filepath.Join(dir, "data", "index-demo.db")); serr != nil {
		t.Fatalf("索引文件未创建: %v", serr)
	}
}

// TestErrMessagesFollowLanguage 哨兵错误的文案跟着当前语言走。
func TestErrMessagesFollowLanguage(t *testing.T) {
	msg := ErrMissingAPIHash.Error()
	if msg == "" {
		t.Fatal("错误文案为空")
	}
	if strings.Contains(msg, "missing api_hash") {
		t.Fatalf("文案未走 i18n: %q", msg)
	}
}

// 输入：构造好的 config.Config 与命令行给的卷名
// 输出：解析出的卷名、卷定义、是否存在
// 预期：无卷段时只有 mytgfs 可解析；有卷段时空名取第一个，未知名报不存在
// TestVolumeResolution 覆盖卷名解析：空名取首个、无 [[volumes]] 段退化为 mytgfs。
//
// 输入：构造好的 config.Config 与命令行给的卷名
// 输出：解析出的卷名、卷定义、是否存在
// 预期：无卷段时只有 mytgfs 可解析；有卷段时空名取第一个，未知名报不存在
func TestVolumeResolution(t *testing.T) {
	multi := &App{cfg: config.Config{
		Telegram: config.Telegram{ChannelID: 111},
		Volumes: []config.Volume{
			{Name: "demo", ChatID: 222},
			{Name: "demo2", ChatID: 333},
		},
	}}
	single := &App{cfg: config.Config{Telegram: config.Telegram{ChannelID: 111}}}

	cases := []struct {
		name     string
		app      *App
		input    string
		wantName string
		wantChat int64
		wantOK   bool
	}{
		{"空名取第一个卷", multi, "", "demo", 222, true},
		{"按名取卷", multi, "demo2", "demo2", 333, true},
		{"未知名", multi, "nope", "nope", 0, false},
		{"无卷段退化默认名", single, "", "mytgfs", 111, true},
		{"无卷段显式默认名", single, "mytgfs", "mytgfs", 111, true},
		{"无卷段其他名", single, "demo", "demo", 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			vol, gotName, ok := c.app.Volume(c.input)
			if gotName != c.wantName {
				t.Fatalf("卷名 = %q，期望 %q", gotName, c.wantName)
			}
			if ok != c.wantOK {
				t.Fatalf("ok = %v，期望 %v", ok, c.wantOK)
			}
			if ok && (vol.ChatID != c.wantChat || vol.Name != c.wantName) {
				t.Fatalf("卷 = %+v，期望 name=%q chat=%d", vol, c.wantName, c.wantChat)
			}
		})
	}
}

// TestChannelsRequiresUserSession 没有 user 会话时直接返回哨兵错误，不连网络。
//
// 输入：Options.UserSessionPath 指向不存在的文件
// 输出：ErrUserSessionMissing
// 预期：getDialogs 只有 user 能做，缺会话时不能退化成空列表骗前端
func TestChannelsRequiresUserSession(t *testing.T) {
	a := New(config.Config{Telegram: config.Telegram{APIID: 1}}, Options{
		UserSessionPath: filepath.Join(t.TempDir(), "user.session"),
	})
	if _, err := a.Channels(context.Background()); !errors.Is(err, ErrUserSessionMissing) {
		t.Fatalf("错误 = %v，期望缺 user 会话", err)
	}
}

// TestChannelsRequiresAPIHash 会话文件存在但缺 api_hash 时也在联网前失败。
func TestChannelsRequiresAPIHash(t *testing.T) {
	path := filepath.Join(t.TempDir(), "user.session")
	if err := os.WriteFile(path, []byte("session"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := New(config.Config{Telegram: config.Telegram{APIID: 1}}, Options{UserSessionPath: path})
	if _, err := a.Channels(context.Background()); !errors.Is(err, ErrMissingAPIHash) {
		t.Fatalf("错误 = %v，期望缺 api_hash", err)
	}
}
