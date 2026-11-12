package logging

// 日志：标准库 log/slog + lumberjack 轮转。
//
// 配置项缺失时使用安全默认值；日志不会无限增长。

import (
	"log/slog"
	"strings"

	"gopkg.in/natefinch/lumberjack.v2"
)

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
		cfg.File = "mytgfs.log"
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
