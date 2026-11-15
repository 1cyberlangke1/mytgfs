package app

// 头像双层缓存的单测：内存缓存不轮询、pts 失效后重建、磁盘兜底与冷启动直读。

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mytgfs/internal/config"
	"mytgfs/internal/telegramstore"
)

// countingStore 在 fakeCoverStore 基础上统计回源次数。
type countingStore struct {
	fakeCoverStore
	imageCalls   int
	versionCalls int
}

func (c *countingStore) CoverImage(context.Context) ([]byte, string, error) {
	c.imageCalls++
	return c.data, c.version, c.err
}

func (c *countingStore) CoverVersion(context.Context) (string, error) {
	c.versionCalls++
	return c.version, c.err
}

// 回归背景：头像字节此前每次都从 Telegram 现下载，面板一直「加载中」。
// 现在内存缓存永不过期（第三方客户端同样不轮询头像），靠 pts 变化失效重建。
func TestCoverImageServedFromMemoryCache(t *testing.T) {
	st := &countingStore{fakeCoverStore: fakeCoverStore{version: "10", data: []byte("img")}}

	provider := &CoverProvider{
		lookup: func(name string) (coverVolume, bool) {
			if name == "demo" {
				return coverVolume{store: st, chatID: 42}, true
			}
			return coverVolume{}, false
		},
		cache: make(map[int64]coverEntry),
	}

	for range 3 {
		data, version, err := provider.CoverImage(t.Context(), "demo")
		if err != nil || version != "10" || !bytes.Equal(data, []byte("img")) {
			t.Fatalf("CoverImage = %q,%q,%v", version, data, err)
		}
	}
	if st.imageCalls != 1 {
		t.Fatalf("回源次数 = %d; want 1（缓存命中不应回源）", st.imageCalls)
	}
}

// SetCover 写入后缓存失效：下一次读取重新回源拿到新版本
func TestCoverImageInvalidatedAfterSetCover(t *testing.T) {
	st := &countingStore{fakeCoverStore: fakeCoverStore{version: "10", data: []byte("old")}}
	provider := &CoverProvider{
		lookup: func(name string) (coverVolume, bool) {
			if name == "demo" {
				return coverVolume{store: st, chatID: 42}, true
			}
			return coverVolume{}, false
		},
		set:   func(context.Context, int64, []byte) error { return nil },
		cache: make(map[int64]coverEntry),
	}
	if _, _, err := provider.CoverImage(t.Context(), "demo"); err != nil {
		t.Fatal(err)
	}

	st.version = "11"
	st.data = []byte("new")
	if err := provider.SetCover(t.Context(), "demo", []byte("new")); err != nil {
		t.Fatal(err)
	}
	_, version, err := provider.CoverImage(t.Context(), "demo")
	if err != nil || version != "11" {
		t.Fatalf("写头像后应回源新版本: version=%q err=%v", version, err)
	}
}

// InvalidateVolume 使缓存失效：下一次读取回源
func TestCoverImageInvalidatedByVolume(t *testing.T) {
	st := &countingStore{fakeCoverStore: fakeCoverStore{version: "10", data: []byte("img")}}
	provider := &CoverProvider{
		lookup: func(name string) (coverVolume, bool) {
			if name == "demo" {
				return coverVolume{store: st, chatID: 42}, true
			}
			return coverVolume{}, false
		},
		cache: make(map[int64]coverEntry),
	}
	if _, _, err := provider.CoverImage(t.Context(), "demo"); err != nil {
		t.Fatal(err)
	}
	st.version = "12"
	st.data = []byte("v12")
	provider.InvalidateVolume("demo")
	_, version, err := provider.CoverImage(t.Context(), "demo")
	if err != nil || version != "12" {
		t.Fatalf("失效后应回源: version=%q err=%v", version, err)
	}
}

// 冷启动：磁盘上有上一张就直接用，不回源 Telegram
func TestCoverImageServesDiskOnColdStart(t *testing.T) {
	cacheDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(cacheDir, "cover-42-100.jpg"), []byte("disk"), 0o600); err != nil {
		t.Fatal(err)
	}
	st := &countingStore{fakeCoverStore: fakeCoverStore{version: "100", data: []byte("fresh")}}
	provider := &CoverProvider{
		lookup: func(name string) (coverVolume, bool) {
			if name == "demo" {
				return coverVolume{store: st, chatID: 42}, true
			}
			return coverVolume{}, false
		},
		cacheDir: cacheDir,
		cache:    make(map[int64]coverEntry),
	}
	data, version, err := provider.CoverImage(t.Context(), "demo")
	if err != nil || version != "100" || !bytes.Equal(data, []byte("disk")) {
		t.Fatalf("冷启动应读磁盘: version=%q data=%q err=%v", version, data, err)
	}
	if st.imageCalls != 0 {
		t.Fatalf("磁盘命中不应回源，回源 %d 次", st.imageCalls)
	}
}

// TestSetConfigAdoptsExternalFileEdit SetConfig 采纳外部文件编辑：文件是唯一权威。
// 场景：CLI 建卷/手编 config.toml 后，运行中的进程靠 SetConfig 收编。
func TestSetConfigAdoptsExternalFileEdit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	base := "[telegram]\napi_id = 1\napi_hash_file = \"h\"\nbot_token_file = \"t\"\n\n[[volumes]]\nname = \"demo\"\nchat_id = 123\nenabled = true\n"
	if err := os.WriteFile(path, []byte(base), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	a := New(cfg, Options{ConfigPath: path})
	if _, _, ok := a.Volume("ext"); ok {
		t.Fatal("外部编辑前 ext 不应存在")
	}

	// 模拟外部编辑：直接改文件（不经过 MutateConfig），再 SetConfig 收编
	ext := strings.Replace(base, "[[volumes]]",
		"[[volumes]]\nname = \"ext\"\nchat_id = 789\nenabled = true\n\n[[volumes]]", 1)
	if err := os.WriteFile(path, []byte(ext), 0o600); err != nil {
		t.Fatal(err)
	}
	fresh, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	a.SetConfig(fresh)

	got, _, ok := a.Volume("ext")
	if !ok || got.ChatID != 789 {
		t.Fatalf("SetConfig 后应看到外部编辑的卷: %+v ok=%v", got, ok)
	}
}

// CoverVersion 缓存命中时零 Telegram 往返：面板反复刷新不打元数据接口
func TestCoverVersionFromMemoryCache(t *testing.T) {
	st := &countingStore{fakeCoverStore: fakeCoverStore{version: "30", data: []byte("img")}}
	provider := &CoverProvider{
		lookup: func(name string) (coverVolume, bool) {
			if name == "demo" {
				return coverVolume{store: st, chatID: 42}, true
			}
			return coverVolume{}, false
		},
		cache: make(map[int64]coverEntry),
	}
	// 预热内存缓存
	if _, _, err := provider.CoverImage(t.Context(), "demo"); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		version, err := provider.CoverVersion(t.Context(), "demo")
		if err != nil || version != "30" {
			t.Fatalf("CoverVersion = %q, %v", version, err)
		}
	}
	if st.versionCalls != 0 {
		t.Fatalf("缓存命中不应回源元数据，回源 %d 次", st.versionCalls)
	}
}

// verifyDisk 核对版本：外部换了头像就回源新字节更新缓存，并报告需要推送
func TestVerifyDiskCorrectsStaleCache(t *testing.T) {
	cacheDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(cacheDir, "cover-42-100.jpg"), []byte("disk"), 0o600); err != nil {
		t.Fatal(err)
	}
	st := &countingStore{fakeCoverStore: fakeCoverStore{version: "200", data: []byte("fresh")}}
	provider := &CoverProvider{
		lookup: func(name string) (coverVolume, bool) {
			if name == "demo" {
				return coverVolume{store: st, chatID: 42}, true
			}
			return coverVolume{}, false
		},
		cacheDir: cacheDir,
		cache:    make(map[int64]coverEntry),
	}

	cv := coverVolume{store: st, chatID: 42}
	if !provider.verifyDisk(t.Context(), cv, "100") {
		t.Fatal("版本变了应回源并更新缓存")
	}
	entry, ok := provider.cache[42]
	if !ok || entry.version != "200" || !bytes.Equal(entry.jpeg, []byte("fresh")) {
		t.Fatalf("缓存应换成新版本: %+v", entry)
	}

	// 版本一致：零下载、零更新
	st.version = "200"
	if provider.verifyDisk(t.Context(), cv, "200") {
		t.Fatal("版本一致不应触发更新")
	}
}

// Telegram 不可达：保持旧缓存，不误更新
func TestVerifyDiskKeepsCacheWhenOffline(t *testing.T) {
	st := &countingStore{fakeCoverStore: fakeCoverStore{version: "100", data: []byte("old"), err: context.DeadlineExceeded}}
	provider := &CoverProvider{
		lookup: func(name string) (coverVolume, bool) {
			if name == "demo" {
				return coverVolume{store: st, chatID: 42}, true
			}
			return coverVolume{}, false
		},
		cache: make(map[int64]coverEntry),
	}
	if provider.verifyDisk(t.Context(), coverVolume{store: st, chatID: 42}, "100") {
		t.Fatal("回源失败不应更新缓存")
	}
}

// 没有磁盘缓存目录时，ErrNoCover 保持原语义：返回空结果而不是错误
func TestCoverImageNoCoverWithoutDisk(t *testing.T) {
	st := &countingStore{fakeCoverStore: fakeCoverStore{err: telegramstore.ErrNoCover}}
	provider := &CoverProvider{
		lookup: func(name string) (coverVolume, bool) {
			if name == "demo" {
				return coverVolume{store: st, chatID: 42}, true
			}
			return coverVolume{}, false
		},
		cache: make(map[int64]coverEntry),
	}
	data, version, err := provider.CoverImage(t.Context(), "demo")
	if err != nil || version != "" || len(data) != 0 {
		t.Fatalf("ErrNoCover 应返回空结果: version=%q data=%q err=%v", version, data, err)
	}
}
