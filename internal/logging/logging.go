package logging

// 日志：标准库 log/slog + lumberjack 轮转。
//
// 配置项缺失时使用安全默认值；日志不会无限增长。

import (
	"log/slog"
	"strings"

	"gopkg.in/natefinch/lumberjack.v2"

	"mytgfs/internal/config"
	"mytgfs/internal/datadir"
	"mytgfs/internal/i18n"
)

// Warn 用当前语言记录一条运行期告警，错误作为结构化属性附带。
//
// 输入：key i18n 键；err 触发错误的 error（可为 nil）；args 文案格式化参数
// 输出：无
// 预期：运行期告警统一走这里写入文件日志，不占用 stdio
func Warn(key string, err error, args ...any) {
	msg := i18n.T(key, args...)
	if err != nil {
		slog.Warn(msg, "err", err)
		return
	}
	slog.Warn(msg)
}

// SetupFromConfig 按 TOML 配置初始化文件日志，供所有进程入口共用。
//
// 输入：cfgPath 配置文件路径
// 输出：无
// 预期：配置缺失时用零值配置，Setup 内部回落安全默认；slog 只写轮转文件
func SetupFromConfig(cfgPath string) {
	cfg, _ := config.Load(cfgPath)
	_, _ = Setup(Config{
		Level:      cfg.Log.Level,
		File:       cfg.Log.File,
		MaxSizeMB:  cfg.Log.MaxSizeMB,
		MaxBackups: cfg.Log.MaxBackups,
		MaxAgeDays: cfg.Log.MaxAgeDays,
	})
}

// Config 是日志配置。
type Config struct {
	Level      string // debug / info / warn / error
	File       string // 日志文件路径
	MaxSizeMB  int    // 单文件最大 MB
	MaxBackups int    // 保留文件数
	MaxAgeDays int    // 保留天数
}

// Setup 初始化全局 slog 与轮转写入器。
//
// 输入：cfg 日志配置（零值用默认）
// 输出：logger 和关闭函数
// 预期：日志超过大小自动轮转，按数量/天数清理旧文件
func Setup(cfg Config) (*slog.Logger, func() error) {
	if cfg.File == "" {
		cfg.File = datadir.Path("mytgfs.log")
	}
	if cfg.MaxSizeMB <= 0 {
		cfg.MaxSizeMB = 10
	}
	if cfg.MaxBackups <= 0 {
		cfg.MaxBackups = 5
	}
	if cfg.MaxAgeDays <= 0 {
		cfg.MaxAgeDays = 7
	}

	w := &lumberjack.Logger{
		Filename:   cfg.File,
		MaxSize:    cfg.MaxSizeMB,
		MaxBackups: cfg.MaxBackups,
		MaxAge:     cfg.MaxAgeDays,
		Compress:   true,
	}

	var level slog.Level
	switch strings.ToLower(strings.TrimSpace(cfg.Level)) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	logger := slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)
	return logger, w.Close
}
