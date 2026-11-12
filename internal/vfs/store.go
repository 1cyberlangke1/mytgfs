package vfs

// Store 是 vfs 依赖的远程存储能力。
//
// 输入/输出：与 telegramstore.Store 的同名方法一致
// 预期：抽成接口后单元测试可用假实现，不必连 Telegram

import (
	"context"
	"io"

	"mytgfs/internal/telegramstore"
)

// Store 抽象 vfs 需要的远程存储方法。
type Store interface {
	Upload(ctx context.Context, r io.Reader, size int64, name, mime, caption string) (*telegramstore.Part, error)
	ReadRange(ctx context.Context, p *telegramstore.Part, offset int64, length int) ([]byte, error)
	Delete(ctx context.Context, ids ...int) error
	Rename(ctx context.Context, messageID int, caption string) error
}
