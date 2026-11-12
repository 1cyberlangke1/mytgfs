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
	Cache     Cache     `toml:"cache"`
	Mount     Mount     `toml:"mount"`
	Ratelimit Ratelimit `toml:"ratelimit"`
	Volumes   []Volume  `toml:"volumes"`
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

// Cache 是块缓存配置。
type Cache struct {
	MaxBytes int64 `toml:"max_bytes"`
}

// Mount 是挂载配置。
type Mount struct {
	Point      string `toml:"point"`
	TotalBytes int64  `toml:"total_bytes"`
}

// Ratelimit 是可选的限速覆盖，零值表示用默认。
type Ratelimit struct {
	AdaptiveMinMS int `toml:"adaptive_min_ms"`
	AdaptiveMaxMS int `toml:"adaptive_max_ms"`
	MaxRetries    int `toml:"max_retries"`
	MaxWaitS      int `toml:"max_wait_s"`
}

// Volume 是一个挂载卷（一个频道）。
type Volume struct {
	Name       string `toml:"name"`
	ChatID     int64  `toml:"chat_id"`
	Mode       string `toml:"mode"`
	Enabled    bool   `toml:"enabled"`
	Layout     string `toml:"layout"`
	OnConflict string `toml:"on_conflict"`
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

// TotalBytes 返回卷显示容量，0 表示 2^63-1。
func (c Config) TotalBytes() int64 {
	if c.Mount.TotalBytes <= 0 {
		return int64(^uint64(0) >> 1)
	}
	return c.Mount.TotalBytes
}

func readTrim(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
