package main

// 面板挂盘控制器的行为测试。
//
// 覆盖两件事：
//  1. 配置 enabled=true 时构造控制器就要把服务拉起来（重启后不丢状态）
//  2. 面板开关能把 enabled 写回配置

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"mytgfs/internal/config"
	"mytgfs/internal/dav"
)

// writeDAVConfig 在临时目录写一份最小配置。
//
// 输入：t 测试句柄；enabled [webdav].enabled 初值
// 输出：配置文件绝对路径
// 预期：卷 demo 已声明，监听地址用随机端口，避免抢占本机端口
func writeDAVConfig(t *testing.T, enabled bool) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	body := `[telegram]
api_id = 1
api_hash_file = "secrets/api_hash"

[[volumes]]
name = "demo"
chat_id = 123
enabled = true

[webdav]
enabled = ` + strconv.FormatBool(enabled) + `
addr = "127.0.0.1:0"
volume = "demo"
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("写配置失败: %v", err)
	}
	return path
}

// testVolumes 返回一个只用于启停的卷表；不访问 Telegram。
func testVolumes() map[string]*webVolume {
	return map[string]*webVolume{
		"demo": {davfs: dav.New(context.Background(), nil, nil, 0)},
	}
}

// TestMountControlAutoStartsWhenEnabled 配置已开启时构造即启动。
//
// 输入：enabled=true
// 输出：svc.State().Enabled 为 true 且地址非空
// 预期：不需要调用 Enable；随后 Disable 能把配置写回 false
func TestMountControlAutoStartsWhenEnabled(t *testing.T) {
	path := writeDAVConfig(t, true)
	svc, ctl := newMountControl(path, testVolumes())
	defer svc.Stop()

	st := svc.State()
	if !st.Enabled || st.Addr == "" {
		t.Fatalf("配置已开启但服务没起来: %+v", st)
	}

	got, err := ctl.Disable()
	if err != nil {
		t.Fatalf("关闭失败: %v", err)
	}
	if got.Enabled {
		t.Fatalf("关闭后状态仍为开启: %+v", got)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("读配置失败: %v", err)
	}
	if cfg.WebDAV.Enabled {
		t.Fatal("关闭后配置仍为 true")
	}
}

// TestMountControlStaysOffWhenDisabled 配置关闭时构造不启动，面板可开启。
//
// 输入：enabled=false
// 输出：构造后未监听；Enable 后监听且配置变 true
// 预期：状态与配置始终保持一致
func TestMountControlStaysOffWhenDisabled(t *testing.T) {
	path := writeDAVConfig(t, false)
	svc, ctl := newMountControl(path, testVolumes())
	defer svc.Stop()

	if st := svc.State(); st.Enabled {
		t.Fatalf("配置关闭却自动启动: %+v", st)
	}

	got, err := ctl.Enable()
	if err != nil {
		t.Fatalf("开启失败: %v", err)
	}
	if !got.Enabled || got.Addr == "" {
		t.Fatalf("开启后状态不对: %+v", got)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("读配置失败: %v", err)
	}
	if !cfg.WebDAV.Enabled {
		t.Fatal("开启后配置仍为 false")
	}
}

// TestMountControlNoVolume 没有可用卷时开启应报错而不是静默成功。
func TestMountControlNoVolume(t *testing.T) {
	path := writeDAVConfig(t, true)
	svc, ctl := newMountControl(path, map[string]*webVolume{})
	defer svc.Stop()

	if st := svc.State(); st.Enabled {
		t.Fatalf("没有卷却启动了服务: %+v", st)
	}
	if _, err := ctl.Enable(); err == nil {
		t.Fatal("没有卷时开启应失败")
	}
}
