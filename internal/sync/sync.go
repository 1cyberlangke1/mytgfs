package sync

import (
	"context"
	"time"

	"mytgfs/internal/index"
)

// Message 是 Telegram 上的一条文件消息。
type Message struct {
	ID            int
	Caption       string
	DocumentID    int64
	AccessHash    int64
	FileReference []byte
	Size          int64
	Date          time.Time
}

// Fetcher 拉取频道消息。
type Fetcher interface {
	History(ctx context.Context, limit int) ([]Message, error)
	HistorySince(ctx context.Context, minID int, limit int) ([]Message, error)
}

// Replay 把消息重放进索引。
//
// 输入：ctx；idx 索引；msgs 消息（顺序无关）
// 输出：写入的路径列表（caption 不合法的消息被跳过）、error
// 预期：只处理带合法 caption 的消息；同 path 保留 message_id 最大者
func Replay(ctx context.Context, idx *index.Index, msgs []Message) ([]string, error) {
	paths := make([]string, 0, len(msgs))
	for _, m := range msgs {
		path, ok := ParseCaption(m.Caption)
		if !ok {
			continue
		}
		f := index.File{
			Path:          path,
			MessageID:     m.ID,
			DocumentID:    m.DocumentID,
			AccessHash:    m.AccessHash,
			FileReference: m.FileReference,
			Size:          m.Size,
			Mtime:         m.Date.Unix(),
		}
		if err := idx.Put(ctx, f); err != nil {
			return paths, err
		}
		paths = append(paths, path)
	}
	return paths, nil
}

// Sync 增量拉取并重放。
//
// 输入：ctx；idx 索引；f 数据源；limit 拉取条数
// 输出：error
// 预期：先读索引最大消息 ID，只拉取更新的消息；拉取失败不修改索引
func Sync(ctx context.Context, idx *index.Index, f Fetcher, limit int) error {
	minID, err := idx.MaxMessageID(ctx)
	if err != nil {
		return err
	}
	msgs, err := f.HistorySince(ctx, minID, limit)
	if err != nil {
		return err
	}
	_, err = Replay(ctx, idx, msgs)
	return err
}
