package config

// PWA 配置：把面板装成桌面应用，并把文件存进浏览器离线缓存。
//
// 默认开启：面板本来就是只绑回环的单页应用，装上独立图标比自己开浏览器顺手。
// 关掉后不输出 manifest、不注册 service worker，退化成普通网页。

import "strings"

// PWA 是「可安装 + 离线」相关配置。
type PWA struct {
	// Enabled 用指针表达三态：未配置(nil) 视为开启；显式 false 才关闭。
	Enabled *bool `toml:"enabled"`
	// Name 是安装后的应用名；空则用默认值。
	Name string `toml:"name"`
	// OfflineMaxMB 是单个文件允许离线保存的上限（MB）：
	// 0 用默认 256，负数表示不限制（浏览器配额仍然生效）。
	OfflineMaxMB int `toml:"offline_max_mb"`
}

// DefaultPWAName 是未配置应用名时使用的名字。
const DefaultPWAName = "mytgfs"

// DefaultPWAOfflineMB 是未配置离线上限时的默认值（MB）。
const DefaultPWAOfflineMB = 256

// On 判断是否启用可安装/离线能力。
//
// 输入：无
// 输出：未配置时 true，显式 false 时 false
// 预期：缺省即好用，用户只有明确关掉才失效
func (p PWA) On() bool {
	if p.Enabled == nil {
		return true
	}
	return *p.Enabled
}

// AppName 返回安装后的应用名。
//
// 输入：无
// 输出：非空名字，默认 mytgfs
// 预期：去掉首尾空白，避免 manifest 里出现空名字
func (p PWA) AppName() string {
	if n := strings.TrimSpace(p.Name); n != "" {
		return n
	}
	return DefaultPWAName
}

// OfflineLimitMB 返回单个文件的离线保存上限（MB）。
//
// 输入：无
// 输出：0 表示不限制，否则为正数 MB
// 预期：未配置用默认 256，显式负数归一成 0（不限制）
func (p PWA) OfflineLimitMB() int {
	switch {
	case p.OfflineMaxMB == 0:
		return DefaultPWAOfflineMB
	case p.OfflineMaxMB < 0:
		return 0
	default:
		return p.OfflineMaxMB
	}
}

// OfflineLimitBytes 返回离线保存上限的字节数；0 表示不限制。
//
// 输入：无
// 输出：字节数
// 预期：MB 换算成 MiB，与前端展示保持一致
func (p PWA) OfflineLimitBytes() int64 {
	mb := p.OfflineLimitMB()
	if mb <= 0 {
		return 0
	}
	return int64(mb) << 20
}
