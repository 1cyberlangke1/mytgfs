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
	Telegram  Telegram  `toml:"telegram"`
	Mount     Mount     `toml:"mount"`
	Volumes   []Volume  `toml:"volumes"`
	Web       Web       `toml:"web"`
	Downloads Downloads `toml:"downloads"`
	Log       Log       `toml:"log"`
}

// Web 是本地面板配置。
type Web struct {
	// Port 监听端口，0 用 DefaultWebPort；被占用时仍会退回随机端口。
	Port int `toml:"port"`
}

// DefaultWebPort 是面板默认端口。
const DefaultWebPort = 8720

// DefaultLogLevel 是日志默认级别。
const DefaultLogLevel = "info"

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

// 运行时默认值：配置缺省、面板展示、实际执行必须共用同一份数字。
const (
	// DefaultSyncSeconds 是自动增量同步的默认间隔秒数。
	DefaultSyncSeconds = 15
	// DefaultDownloadParallel 是下载并发连接数的默认值。
	DefaultDownloadParallel = 4
	// DefaultReadChunkMB 是每次向上游取数的分片大小默认值（MB）。
	DefaultReadChunkMB = 8
	// DefaultUploadParallel 是上传并发分片数的默认值。
	DefaultUploadParallel = 4
)

// Mount 是挂载配置。
type Mount struct {
	// SyncSeconds 是托盘自动增量同步的间隔秒数：0 用 DefaultSyncSeconds，负数关闭。
	SyncSeconds int `toml:"sync_seconds"`
	// DownloadParallel 是下载并发连接数：0 用 DefaultDownloadParallel，范围 1..8。
	DownloadParallel int `toml:"download_parallel"`
	// ReadChunkMB 是每次向上游取数的分片大小（MB）：0 用 DefaultReadChunkMB，范围 1..64。
	ReadChunkMB int `toml:"read_chunk_mb"`
	// UploadParallel 是上传并发分片数：0 用 DefaultUploadParallel，范围 1..16。
	UploadParallel int `toml:"upload_parallel"`
}

// UploadParallelism 返回上传并发分片数。
//
// 输入：无
// 输出：并发数，默认 DefaultUploadParallel，夹在 1..16
// 预期：网络好的可以调大，慢网络调小；与下载并发独立
func (c Mount) UploadParallelism() int {
	n := c.UploadParallel
	if n <= 0 {
		return DefaultUploadParallel
	}
	if n > 16 {
		return 16
	}
	return n
}

// ReadChunk 返回读分片大小（字节）。
//
// 输入：无
// 输出：字节数，默认 DefaultReadChunkMB，夹在 1..64MB
// 预期：网络快的可以调大减少请求数，网络慢的调小降低等待
func (c Mount) ReadChunk() int64 {
	mb := c.ReadChunkMB
	if mb <= 0 {
		mb = DefaultReadChunkMB
	}
	if mb > 64 {
		mb = 64
	}
	return int64(mb) << 20
}

// DownloadConnections 返回下载连接数。
//
// 输入：无
// 输出：连接数，默认 DefaultDownloadParallel，夹在 1..8
// 预期：单条 MTProto 连接吃不满带宽，多条才有效；过多会互相挤占
func (m Mount) DownloadConnections() int {
	n := m.DownloadParallel
	if n <= 0 {
		return DefaultDownloadParallel
	}
	if n > 8 {
		return 8
	}
	return n
}

// Volume 是一个卷（一个频道）。
type Volume struct {
	Name    string `toml:"name"`
	ChatID  int64  `toml:"chat_id"`
	Enabled bool   `toml:"enabled"`
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
