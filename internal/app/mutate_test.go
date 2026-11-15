package app_test

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"mytgfs/internal/app"
	"mytgfs/internal/config"
)

// 回归背景：建卷只写配置文件、不动 App 内存副本，OpenVolume 按旧卷表查不到新卷，
// 日志实证 err="volume not found: 2"，新卷要重启才出现在面板。
// MutateConfig 是配置变更的唯一入口：改盘与换内存一步完成，两端不可能再脱节。
const baseCfg = `[telegram]
api_id = 1
api_hash_file = "secrets/api_hash"
bot_token_file = "secrets/bot_token"

[[volumes]]
name = "demo"
chat_id = 123
enabled = true
`

func writeCfg(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestMutateConfigKeepsMemoryInSync(t *testing.T) {
	path := writeCfg(t, baseCfg)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	a := app.New(cfg, app.Options{ConfigPath: path})

	if _, _, ok := a.Volume("v2"); ok {
		t.Fatal(" Mutate 之前 v2 不应存在")
	}

	// 通过唯一写入口加卷：文件与内存必须同时可见
	vol := config.Volume{Name: "v2", ChatID: 456, Enabled: true}
	if err := a.MutateConfig(func(p string) error {
		return config.AppendVolume(p, vol)
	}); err != nil {
		t.Fatal(err)
	}

	got, _, ok := a.Volume("v2")
	if !ok {
		t.Fatal("MutateConfig 后 Volume(\"v2\") 仍找不到：内存副本没跟上")
	}
	if got.ChatID != 456 {
		t.Fatalf("ChatID = %d; want 456", got.ChatID)
	}
	// 磁盘上也要真的写进去了
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"v2"`) && !strings.Contains(string(body), "v2") {
		t.Fatalf("配置文件里没有 v2:\n%s", body)
	}
}

// 删卷同理：文件与内存一起消失
func TestMutateConfigRemoveKeepsMemoryInSync(t *testing.T) {
	path := writeCfg(t, baseCfg)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	a := app.New(cfg, app.Options{ConfigPath: path})

	if err := a.MutateConfig(func(p string) error {
		return config.RemoveVolume(p, "demo")
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := a.Volume("demo"); ok {
		t.Fatal("删卷后内存副本里 demo 仍在")
	}
}

// 并发读与并发变更不出现数据竞争（-race 下跑）
func TestMutateConfigConcurrent(t *testing.T) {
	path := writeCfg(t, baseCfg)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	a := app.New(cfg, app.Options{ConfigPath: path})

	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			n := i
			_ = a.MutateConfig(func(p string) error {
				return config.AppendVolume(p, config.Volume{Name: "v" + string(rune('a'+n)), ChatID: int64(n), Enabled: true})
			})
		}()
		go func() {
			defer wg.Done()
			_ = a.Config()
			_, _, _ = a.Volume("demo")
		}()
	}
	wg.Wait()
}
