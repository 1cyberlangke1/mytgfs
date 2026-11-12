package telegramstore

import "errors"

var (
	// ErrTooLarge 表示文件超过单条 Telegram 消息上限。
	ErrTooLarge = errors.New("file exceeds Telegram single-message limit")
	// ErrNoDocument 表示 Updates 里没有 document。
	ErrNoDocument = errors.New("updates contains no document")
	// ErrNoMessage 表示 Updates 里没有消息 ID。
	ErrNoMessage = errors.New("updates contains no message id")
	// ErrBadRange 表示读取区间越界。
	ErrBadRange = errors.New("range out of bounds")
)
