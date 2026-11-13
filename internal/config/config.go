package config

// TOML 配置读取。
//
// 所有可选字段缺失时保持零值，由调用方决定默认行为。

import (
	"os"
	"strings"

	"github.com/BurntSushi/toml"
)

// Config 是顶层配置。
type Config struct {
	Telegram Telegram `toml:"telegram"`
	Mount    Mount    `toml:"mount"`
	Volumes  []Volume `toml:"volumes"`
	Web      Web      `toml:"web"`
	WebDAV   WebDAV   `toml:"webdav"`
	PWA      PWA      `toml:"pwa"`
	Log      Log      `toml:"log"`
}

// Web 是本地面板配置。
type Web struct {
	// Port 监听端口，0 用 DefaultWebPort；被占用时仍会退回随机端口。
	Port int `toml:"port"`
	// AutoOpen 打开面板时是否同时打开浏览器；nil 按 true 处理。
	AutoOpen *bool `toml:"auto_open"`
}

// DefaultWebPort 是面板默认端口。
const DefaultWebPort = 8720

// ListenPort 返回面板端口。
//
// 输入：无
// 输出：端口号，默认 DefaultWebPort，越界时同样回落默认值
// 预期：只用于 127.0.0.1 回环监听，端口冲突由调用方退回随机端口
func (w Web) ListenPort() int {
	if w.Port <= 0 || w.Port > 65535 {
		return DefaultWebPort
	}
	return w.Port
}

// OpensBrowser 表示打开面板时是否自动开浏览器。
//
// 输入：无
// 输出：true 表示起面板后直接开系统浏览器
// 预期：未写 auto_open 时按 true 处理；关掉后只启动服务，等用户自己访问
func (w Web) OpensBrowser() bool {
	return w.AutoOpen == nil || *w.AutoOpen
}

// WebDAV 是「把卷挂成盘」的可选服务。
//
// 默认关闭（Enabled=false）：面板本身已经能做增删改查，
// 只有确实要用资源管理器/rclone 挂盘时才需要打开。
type WebDAV struct {
	Enabled bool `toml:"enabled"`
	// Addr 监听地址，空则用 DefaultWebDAVAddr
	Addr string `toml:"addr"`
	// Volume 只暴露这个卷；空则用第一个启用的卷
	Volume string `toml:"volume"`
}

// DefaultWebDAVAddr 是 WebDAV 默认监听地址。
const DefaultWebDAVAddr = "127.0.0.1:8080"

// ListenAddr 返回实际监听地址。
//
// 输入：无
// 输出：非空地址，默认 127.0.0.1:8080
// 预期：只绑本地回环，不对外网暴露
func (w WebDAV) ListenAddr() string {
	if a := strings.TrimSpace(w.Addr); a != "" {
		return a
	}
	return DefaultWebDAVAddr
}

// Target 挑出要暴露的卷。
//
// 输入：volumes 配置里的卷
// 输出：卷与是否存在
// 预期：
//   - Volume 非空时按名字精确匹配（找不到就 false，不静默换卷）
//   - Volume 为空时取第一个 enabled 的卷
func (w WebDAV) Target(volumes []Volume) (Volume, bool) {
	want := strings.TrimSpace(w.Volume)
	if want != "" {
		for _, v := range volumes {
			if strings.EqualFold(v.Name, want) {
				return v, true
			}
		}
		return Volume{}, false
	}
	for _, v := range volumes {
		if v.Enabled {
			return v, true
		}
	}
	return Volume{}, false
}

// Log 是日志配置。
type Log struct {
	Level      string `toml:"level"`
	File       string `toml:"file"`
	MaxSizeMB  int    `toml:"max_size_mb"`
	MaxBackups int    `toml:"max_backups"`
	MaxAgeDays int    `toml:"max_age_days"`
}

// Telegram 是账号与频道配置。
type Telegram struct {
	APIID        int    `toml:"api_id"`
	APIHashFile  string `toml:"api_hash_file"`
	BotTokenFile string `toml:"bot_token_file"`
	ChannelID    int64  `toml:"channel_id"`
	AccessHash   int64  `toml:"access_hash"`
	ChatID       int64  `toml:"chat_id"`
}

// Mount 是挂载配置。
type Mount struct {
	Point string `toml:"point"`
	// SyncSeconds 是托盘自动增量同步的间隔秒数：0 用默认 15，负数关闭。
	SyncSeconds int `toml:"sync_seconds"`
	// DownloadParallel 是下载并发块数：0 用默认 8，范围 1..32。
	DownloadParallel int `toml:"download_parallel"`
	// ReadChunkMB 是 WebDAV 每次向上游取数的分片大小（MB）：0 用默认 8，范围 1..64。
	ReadChunkMB int `toml:"read_chunk_mb"`
	// UploadParallel 是上传并发分片数：0 用默认 4，范围 1..16。
	UploadParallel int `toml:"upload_parallel"`
}

// UploadParallelism 返回上传并发分片数。
//
// 输入：无
// 输出：并发数，默认 4，夹在 1..16
// 预期：网络好的可以调大，慢网络调小；与下载并发独立
func (c Mount) UploadParallelism() int {
	n := c.UploadParallel
	if n <= 0 {
		return 4
	}
	if n > 16 {
		return 16
	}
	return n
}

// ReadChunk 返回 WebDAV 读分片大小（字节）。
//
// 输入：无
// 输出：字节数，默认 1MB，夹在 1..64MB
// 预期：网络快的可以调大减少请求数，网络慢的调小降低等待
func (c Mount) ReadChunk() int64 {
	mb := c.ReadChunkMB
	if mb <= 0 {
		mb = 8
	}
	if mb > 64 {
		mb = 64
	}
	return int64(mb) << 20
}

// DownloadConnections 返回下载连接数。
//
// 输入：无
// 输出：连接数，默认 4，夹在 1..8
// 预期：单条 MTProto 连接吃不满带宽，多条才有效；过多会互相挤占
func (m Mount) DownloadConnections() int {
	n := m.DownloadParallel
	if n <= 0 {
		return 4
	}
	if n > 8 {
		return 8
	}
	return n
}

// Volume 是一个挂载卷（一个频道）。
type Volume struct {
	Name    string `toml:"name"`
	ChatID  int64  `toml:"chat_id"`
	Enabled bool   `toml:"enabled"`
	Point   string `toml:"point"`
}

// Load 读取 TOML 配置。
//
// 输入：path 配置文件
// 输出：Config
// 预期：文件不存在或语法错误时返回 error
func Load(path string) (Config, error) {
	var c Config
	if _, err := toml.DecodeFile(path, &c); err != nil {
		return c, err
	}
	return c, nil
}

// APIHash 读取 api_hash 文件内容。
func (c Config) APIHash() string { return readTrim(c.Telegram.APIHashFile) }

// BotToken 读取 bot token 文件内容。
func (c Config) BotToken() string { return readTrim(c.Telegram.BotTokenFile) }

func readTrim(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
