package fsck

// 索引对账：把本地索引行与频道里的消息逐条比对。
//
// 索引是可丢弃的缓存，但 Telegram 侧也可能被手工改动（删消息、改 caption），
// 自动同步覆盖不到的漂移需要一次显式核对，并可选择就地修复。

import (
	"context"
	"fmt"

	"github.com/gotd/td/tg"

	"mytgfs/internal/index"
	tgsync "mytgfs/internal/sync"
)

// 问题类型。
const (
	// KindMissing 表示索引行对应的消息已不存在（或不再是文件消息）。
	KindMissing = "missing"
	// KindMoved 表示 caption 记录的路径与索引不同。
	KindMoved = "moved"
)

// batchSize 是单次 channels.getMessages 请求的消息数上限。
const batchSize = 100

// API 是对账需要的 Telegram 能力。
type API interface {
	ChannelsGetMessages(ctx context.Context, req *tg.ChannelsGetMessagesRequest) (tg.MessagesMessagesClass, error)
}

// Problem 是一条漂移记录。
type Problem struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	MsgID  int    `json:"message_id"`
	Actual string `json:"actual,omitempty"`
}

// Report 是对账结果。
type Report struct {
	Checked  int
	Missing  int
	Moved    int
	Fixed    int
	Problems []Problem
}

// Run 逐条核对索引行对应的频道消息。
//
// 输入：ctx；idx 本地索引；api bot 会话；channelID/accessHash 频道；fix 是否修复
// 输出：统计与问题列表；索引读写或 RPC 错误原样返回
// 预期：
//   - 消息已不存在或 caption 不是本程序格式 => KindMissing；fix 时删掉该索引行
//   - caption 里的路径与索引不同 => KindMoved；fix 时把行迁到真实路径
//   - 一致的行保持不动
func Run(ctx context.Context, idx *index.Index, api API, channelID, accessHash int64, fix bool) (Report, error) {
	var rep Report
	files, err := idx.ListPrefix(ctx, "/")
	if err != nil {
		return rep, err
	}
	rep.Checked = len(files)
	for start := 0; start < len(files); start += batchSize {
		end := start + batchSize
		if end > len(files) {
			end = len(files)
		}
		batch := files[start:end]
		byID, ferr := fetch(ctx, api, channelID, accessHash, batch)
		if ferr != nil {
			return rep, ferr
		}
		for _, f := range batch {
			msg, ok := byID[f.MessageID]
			if !ok {
				rep.Missing++
				rep.Problems = append(rep.Problems, Problem{Path: f.Path, Kind: KindMissing, MsgID: f.MessageID})
				if fix {
					if derr := idx.Delete(ctx, f.Path); derr != nil {
						return rep, derr
					}
					rep.Fixed++
				}
				continue
			}
			p, _, pok := tgsync.ParseFile(msg.Message)
			if !pok {
				// 消息还在，但不是本程序写的文件消息：索引行已无对应元数据
				rep.Missing++
				rep.Problems = append(rep.Problems, Problem{Path: f.Path, Kind: KindMissing, MsgID: f.MessageID})
				if fix {
					if derr := idx.Delete(ctx, f.Path); derr != nil {
						return rep, derr
					}
					rep.Fixed++
				}
				continue
			}
			if p == f.Path {
				continue
			}
			rep.Moved++
			rep.Problems = append(rep.Problems, Problem{Path: f.Path, Kind: KindMoved, MsgID: f.MessageID, Actual: p})
			if fix {
				if derr := idx.Delete(ctx, f.Path); derr != nil {
					return rep, derr
				}
				f.Path = p
				if perr := idx.Replace(ctx, f); perr != nil {
					return rep, perr
				}
				rep.Fixed++
			}
		}
	}
	return rep, nil
}

// fetch 批量取消息，返回 消息 ID 到消息的映射；已删除的消息不会出现在结果里。
func fetch(ctx context.Context, api API, channelID, accessHash int64, batch []index.File) (map[int]*tg.Message, error) {
	ids := make([]tg.InputMessageClass, 0, len(batch))
	for _, f := range batch {
		ids = append(ids, &tg.InputMessageID{ID: f.MessageID})
	}
	res, err := api.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{
		Channel: &tg.InputChannel{ChannelID: channelID, AccessHash: accessHash},
		ID:      ids,
	})
	if err != nil {
		return nil, fmt.Errorf("getMessages: %w", err)
	}
	out := make(map[int]*tg.Message, len(batch))
	for _, mc := range messagesOf(res) {
		// MessageEmpty 表示消息已被删除，不进映射
		if m, ok := mc.(*tg.Message); ok {
			out[m.ID] = m
		}
	}
	return out, nil
}

// messagesOf 把 MessagesMessagesClass 的三种形态摊平成消息列表。
func messagesOf(res tg.MessagesMessagesClass) []tg.MessageClass {
	switch m := res.(type) {
	case *tg.MessagesMessages:
		return m.Messages
	case *tg.MessagesMessagesSlice:
		return m.Messages
	case *tg.MessagesChannelMessages:
		return m.Messages
	}
	return nil
}
