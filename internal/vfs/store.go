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
	ReadRange(ctx context.Context, p *telegramstore.Part, offset int64, length int, progress func(done int64)) ([]byte, error)
	Delete(ctx context.Context, ids ...int) error
	Rename(ctx context.Context, messageID int, caption string) error
	// Refresh 按消息 ID 重新拉取 document 元数据；file_reference 过期时用
	Refresh(ctx context.Context, messageID int) (*telegramstore.Part, error)
	// Copy 复用已有 document 发一条新消息，不传输文件内容；用于复制与去重
	Copy(ctx context.Context, src *telegramstore.Part, name, mime, caption string) (*telegramstore.Part, error)
}
