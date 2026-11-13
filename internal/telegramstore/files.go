package telegramstore

// 按消息号批量取节点内容（名字、父消息号、文件元数据）。

import (
	"context"

	"github.com/gotd/td/tg"

	"mytgfs/internal/dirent"
)

// metadataBatch 是单次 channels.getMessages 的消息数上限。
const metadataBatch = 100

// NodeInfo 是一条远端节点消息的完整内容。
type NodeInfo struct {
	MessageID int
	Name      string
	Parent    int
	IsDir     bool
	Sha       string
	// 以下字段只有文件节点有意义。
	DocumentID    int64
	AccessHash    int64
	FileReference []byte
	Size          int64
	Mtime         int64
}

// NodeOf 把一条 Telegram 消息与它的正文解析成节点描述。
//
// 输入：msg 消息；body 正文
// 输出：节点内容；ok=false 表示不是本程序的节点消息
// 预期：文件节点必须带 document，目录节点忽略 document
func NodeOf(msg *tg.Message, body string) (NodeInfo, bool) {
	n, ok := dirent.Parse(body)
	if !ok {
		return NodeInfo{}, false
	}
	info := NodeInfo{
		MessageID: msg.ID,
		Name:      n.Name,
		Parent:    n.Parent,
		IsDir:     n.Dir,
		Sha:       n.Sha,
		Mtime:     int64(msg.Date),
	}
	if n.Dir {
		return info, true
	}
	doc, ok := documentOf(msg.Media)
	if !ok {
		return NodeInfo{}, false
	}
	info.DocumentID = doc.ID
	info.AccessHash = doc.AccessHash
	info.FileReference = doc.FileReference
	info.Size = doc.Size
	return info, true
}

// fetchMessages 批量拉消息，返回 消息号 → 消息。
//
// 输入：ctx；ids 消息号（可超过 100，内部自动分批）
// 输出：存在的消息；error
// 预期：已删除的消息不出现在结果里，不当作错误
func (s *Store) fetchMessages(ctx context.Context, ids []int) (map[int]*tg.Message, error) {
	out := make(map[int]*tg.Message, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	ch, err := s.resolve(ctx)
	if err != nil {
		return nil, err
	}
	channel := &tg.InputChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash}
	for start := 0; start < len(ids); start += metadataBatch {
		end := min(start+metadataBatch, len(ids))
		batch := make([]tg.InputMessageClass, 0, end-start)
		for _, id := range ids[start:end] {
			batch = append(batch, &tg.InputMessageID{ID: id})
		}
		res, err := s.api.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{
			Channel: channel,
			ID:      batch,
		})
		if err != nil {
			return nil, DescribeError(s.noteErr(err))
		}
		for _, mc := range messagesOf(res) {
			if msg, ok := mc.(*tg.Message); ok {
				out[msg.ID] = msg
			}
		}
	}
	return out, nil
}

// FetchNodes 批量读取节点消息。
//
// 输入：ctx；ids 消息号
// 输出：消息号 → 节点内容；不是节点消息的 id 不出现
// 预期：消息已被删除时跳过
func (s *Store) FetchNodes(ctx context.Context, ids []int) (map[int]NodeInfo, error) {
	msgs, err := s.fetchMessages(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make(map[int]NodeInfo, len(msgs))
	for id, msg := range msgs {
		if info, ok := NodeOf(msg, msg.Message); ok {
			out[id] = info
		}
	}
	return out, nil
}
