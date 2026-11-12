package volume

// 卷 = Telegram 私有频道 + 本地索引文件。
//
// 本包只负责在 Telegram 侧把一个卷建到可用状态：
// 建私有频道 → 拉 bot 进频道并给管理权限 → 永久关闭该频道通知。
// 配置落盘由 internal/config 负责，挂载由 vfs 负责。

import (
	"context"
	"fmt"
	"strings"

	"mytgfs/internal/channel"
)

// Request 是建卷参数。
type Request struct {
	Name        string // 卷名：索引 index-<name>.db 与配置里的 name
	Title       string // 频道标题，空则用 Name
	BotUsername string // bot 用户名，不含 @
}

// Result 是建卷结果。
type Result struct {
	Name       string
	ChannelID  int64
	AccessHash int64
	ChatID     int64 // Bot API 风格 -100... 形式，仅展示与配置用
	Title      string
	BotID      int64
	Muted      bool  // 通知是否关闭成功；失败不影响卷可用
	MuteErr    error // 关通知失败原因，Muted 为 true 时是 nil
}

// Create 建卷：创建私有频道 → 拉 bot 进频道并提权 → 永久关闭通知。
//
// 输入：ctx；api 用 user 会话调用的 Telegram API；req 建卷参数
// 输出：Result；关通知失败不算致命，记在 Muted/MuteErr 里
// 预期：建频道或拉 bot 失败直接返回 error；频道可能已经建出来，
//
//	调用方应把 channel id 提示给用户，便于手动清理
func Create(ctx context.Context, api channel.API, req Request) (Result, error) {
	req.Name = strings.TrimSpace(req.Name)
	req.Title = strings.TrimSpace(req.Title)
	req.BotUsername = strings.TrimPrefix(strings.TrimSpace(req.BotUsername), "@")
	if req.Name == "" {
		return Result{}, fmt.Errorf("卷名不能为空")
	}
	if req.BotUsername == "" {
		return Result{}, fmt.Errorf("bot 用户名不能为空")
	}
	if req.Title == "" {
		req.Title = req.Name
	}

	ch, err := channel.Create(ctx, api, req.Title)
	if err != nil {
		return Result{}, err
	}
	partial := Result{
		Name:       req.Name,
		ChannelID:  ch.ChannelID,
		AccessHash: ch.AccessHash,
		ChatID:     ChatID(ch.ChannelID),
		Title:      ch.Title,
	}

	botID, err := channel.AddBot(ctx, api, ch, req.BotUsername)
	if err != nil {
		return partial, fmt.Errorf("频道 %d 已创建，但拉 bot 失败: %w", ch.ChannelID, err)
	}
	partial.BotID = botID

	if err := channel.MuteForever(ctx, api, ch); err != nil {
		partial.Muted = false
		partial.MuteErr = err
		return partial, nil
	}
	partial.Muted = true
	return partial, nil
}

// ChatID 把 channel ID 转成 Bot API 风格的 -100... 形式。
//
// 输入：channelID 频道 id
// 输出：-1000000000000-channelID
// 预期：与 Bot API 的 chat_id 表示一致，仅用于展示与配置
func ChatID(channelID int64) int64 {
	return -1000000000000 - channelID
}
