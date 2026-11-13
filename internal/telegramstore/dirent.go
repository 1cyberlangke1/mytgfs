package telegramstore

// 目录项的远端读写。
//
// 远端模型：每个节点一条消息。目录消息正文是目录项表，溢出部分放在后续的文本消息里，
// 正文过长时目录消息只留 {"ref":id}，真正内容在另一条消息里。

import (
	"context"
	"fmt"

	"github.com/gotd/td/tg"

	"mytgfs/internal/dirent"
)

// chunkBudget 是单条目录消息携带目录项的字节预算，留给正文包装与续块指针。
const chunkBudget = dirent.ChunkThreshold - 64

// maxChunkHops 是读取目录链的跳数上限，防止数据异常时死循环。
const maxChunkHops = 512

// DirContent 是一个目录在远端的完整状态。
type DirContent struct {
	// Entries 是链上所有目录项，按原顺序。
	Entries []dirent.Entry
	// Chunks 是续块消息号，按链顺序。
	Chunks []int
	// RefMsg 是存放正文的替身消息号；0 表示正文就写在目录消息里。
	RefMsg int
}

// PinnedRoot 返回频道置顶消息号。
//
// 输入：ctx
// 输出：置顶消息号；没有置顶返回 0
// 预期：根目录锚点就是置顶消息，空卷时返回 0 由调用方决定是否初始化
func (s *Store) PinnedRoot(ctx context.Context) (int, error) {
	ch, err := s.resolve(ctx)
	if err != nil {
		return 0, err
	}
	full, err := s.api.ChannelsGetFullChannel(ctx, &tg.InputChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash})
	if err != nil {
		return 0, DescribeError(s.noteErr(err))
	}
	cf, ok := full.FullChat.(*tg.ChannelFull)
	if !ok {
		return 0, nil
	}
	id, _ := cf.GetPinnedMsgID()
	return id, nil
}

// PinRoot 静默置顶一条消息，作为根目录锚点。
//
// 输入：ctx；id 消息号
// 输出：error
// 预期：不产生通知；置顶失败原样返回错误
func (s *Store) PinRoot(ctx context.Context, id int) error {
	ch, err := s.resolve(ctx)
	if err != nil {
		return err
	}
	req := &tg.MessagesUpdatePinnedMessageRequest{
		Peer: &tg.InputPeerChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash},
		ID:   id,
	}
	req.SetSilent(true)
	if err := s.throttle(ctx); err != nil {
		return err
	}
	if _, err := s.api.MessagesUpdatePinnedMessage(ctx, req); err != nil {
		return DescribeError(s.noteErr(err))
	}
	return nil
}

// SendText 发一条纯文本消息并返回消息号。
//
// 输入：ctx；text 正文
// 输出：消息号；error
// 预期：走与文件消息相同的发送节流，避免触发频道限速
func (s *Store) SendText(ctx context.Context, text string) (int, error) {
	ch, err := s.resolve(ctx)
	if err != nil {
		return 0, err
	}
	return s.sendMeta(ctx, &tg.InputPeerChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash}, text)
}

// EditText 改写一条文本消息的正文。
//
// 输入：ctx；id 消息号；text 新正文
// 输出：error
// 预期：只改正文，不动其他属性；消息已不存在时返回错误
func (s *Store) EditText(ctx context.Context, id int, text string) error {
	ch, err := s.resolve(ctx)
	if err != nil {
		return err
	}
	req := &tg.MessagesEditMessageRequest{
		Peer: &tg.InputPeerChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash},
		ID:   id,
	}
	req.SetMessage(text)
	if err := s.throttle(ctx); err != nil {
		return err
	}
	if _, err := s.api.MessagesEditMessage(ctx, req); err != nil {
		return DescribeError(s.noteErr(err))
	}
	return nil
}

// Text 读取一条消息的正文。
//
// 输入：ctx；id 消息号
// 输出：正文；error
// 预期：消息不存在返回 ErrNoMessage
func (s *Store) Text(ctx context.Context, id int) (string, error) {
	ch, err := s.resolve(ctx)
	if err != nil {
		return "", err
	}
	res, err := s.api.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{
		Channel: &tg.InputChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash},
		ID:      []tg.InputMessageClass{&tg.InputMessageID{ID: id}},
	})
	if err != nil {
		return "", DescribeError(s.noteErr(err))
	}
	for _, mc := range messagesOf(res) {
		if msg, ok := mc.(*tg.Message); ok {
			return msg.Message, nil
		}
	}
	return "", ErrNoMessage
}

// CreateDir 新建一个空目录消息。
//
// 输入：ctx
// 输出：目录消息号；error
// 预期：正文是空目录项表，后续由 UpdateDir 填充
func (s *Store) CreateDir(ctx context.Context) (int, error) {
	body, err := dirent.EncodeDir(nil, 0)
	if err != nil {
		return 0, err
	}
	return s.SendText(ctx, body)
}

// ReadDir 读取目录的完整目录项（含续块链）。
//
// 输入：ctx；headID 目录消息号
// 输出：目录内容；error
// 预期：
//   - 正文是 {"ref":id} 时先取替身消息再解析
//   - 沿 n 指针读完所有续块，跳数超过 maxChunkHops 报错
//   - 不是目录消息时报错
func (s *Store) ReadDir(ctx context.Context, headID int) (DirContent, error) {
	out := DirContent{}
	raw, err := s.Text(ctx, headID)
	if err != nil {
		return out, err
	}
	if refID, ok := dirent.IsRef(raw); ok {
		out.RefMsg = refID
		if raw, err = s.Text(ctx, refID); err != nil {
			return out, err
		}
	}
	kind, entries, next, err := dirent.ParseDir(raw)
	if err != nil {
		return out, err
	}
	if kind != dirent.TypeDir {
		return out, fmt.Errorf("telegramstore: message %d is not a directory", headID)
	}
	out.Entries = append(out.Entries, entries...)
	hops := 0
	for next != 0 {
		hops++
		if hops > maxChunkHops {
			return out, fmt.Errorf("telegramstore: directory %d chunk chain too long", headID)
		}
		cur := next
		if raw, err = s.Text(ctx, cur); err != nil {
			return out, err
		}
		kind, entries, next, err = dirent.ParseDir(raw)
		if err != nil {
			return out, err
		}
		if kind != dirent.TypeChunk {
			return out, fmt.Errorf("telegramstore: message %d is not a chunk", cur)
		}
		out.Chunks = append(out.Chunks, cur)
		out.Entries = append(out.Entries, entries...)
	}
	return out, nil
}

// UpdateDir 把目录项写回远端，返回新的目录状态。
//
// 输入：ctx；headID 目录消息号；old 读取时的状态（复用续块与替身消息，避免留孤儿）；
// entries 新目录项
// 输出：新的 DirContent；error
// 预期：
//   - 单块且正文不超过 900 字节：直接写在目录消息里
//   - 单块但正文过长：内容写进替身消息，目录消息留 {"ref":id}
//   - 多块：首块放目录消息（过长同样走替身），其余按续块链写
//   - 用不到的旧续块/旧替身消息会被删除
func (s *Store) UpdateDir(ctx context.Context, headID int, old DirContent, entries []dirent.Entry) (DirContent, error) {
	blocks, err := dirent.SplitEntries(entries, chunkBudget)
	if err != nil {
		return DirContent{}, err
	}
	need := len(blocks) - 1

	ids := make([]int, 0, need)
	for i := 0; i < need && i < len(old.Chunks); i++ {
		ids = append(ids, old.Chunks[i])
	}
	for len(ids) < need {
		id, err := s.SendText(ctx, "{}")
		if err != nil {
			return DirContent{}, err
		}
		ids = append(ids, id)
	}

	for i, id := range ids {
		next := 0
		if i+1 < len(ids) {
			next = ids[i+1]
		}
		body, err := dirent.EncodeChunk(blocks[i+1], next)
		if err != nil {
			return DirContent{}, err
		}
		if err := s.EditText(ctx, id, body); err != nil {
			return DirContent{}, err
		}
	}
	if drop := old.Chunks[min(need, len(old.Chunks)):]; len(drop) > 0 {
		if err := s.Delete(ctx, drop...); err != nil {
			return DirContent{}, err
		}
	}

	firstNext := 0
	if len(ids) > 0 {
		firstNext = ids[0]
	}
	headBody, err := dirent.EncodeDir(blocks[0], firstNext)
	if err != nil {
		return DirContent{}, err
	}

	refMsg := 0
	if dirent.NeedsRef(headBody) {
		refMsg = old.RefMsg
		if refMsg == 0 {
			if refMsg, err = s.SendText(ctx, headBody); err != nil {
				return DirContent{}, err
			}
		} else if err := s.EditText(ctx, refMsg, headBody); err != nil {
			return DirContent{}, err
		}
		placeholder, err := dirent.EncodeRef(refMsg)
		if err != nil {
			return DirContent{}, err
		}
		if err := s.EditText(ctx, headID, placeholder); err != nil {
			return DirContent{}, err
		}
	} else {
		if err := s.EditText(ctx, headID, headBody); err != nil {
			return DirContent{}, err
		}
		if old.RefMsg != 0 {
			if err := s.Delete(ctx, old.RefMsg); err != nil {
				return DirContent{}, err
			}
		}
	}

	return DirContent{Entries: entries, Chunks: ids, RefMsg: refMsg}, nil
}
