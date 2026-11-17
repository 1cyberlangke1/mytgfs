package config

// 默认文件布局。
//
// 会话、密钥与凭证都在 secrets/ 下；app / authsvc / CLI 共用这一份定义，
// 避免同一个文件名在多个包里各写一遍字符串字面量。

const (
	// DefaultConfigPath 是配置文件相对路径。
	DefaultConfigPath = "config.toml"
	// DefaultAPIHashFile 是 api_hash 文本文件。
	DefaultAPIHashFile = "secrets/api_hash"
	// DefaultBotTokenFile 是 bot token 文本文件。
	DefaultBotTokenFile = "secrets/bot_token"
	// DefaultBotSession 是 bot 会话文件。
	DefaultBotSession = "secrets/bot.session"
	// DefaultUserSession 是 user 会话文件。
	DefaultUserSession = "secrets/user.session"
	// DefaultKeyPath 是会话加密密钥文件。
	DefaultKeyPath = "secrets/session.key"
)
