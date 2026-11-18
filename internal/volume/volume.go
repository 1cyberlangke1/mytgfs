package volume

// 卷 = Telegram 私有频道 + 本地索引文件。
//
// 本包只负责在 Telegram 侧把一个卷建到可用状态：
// 建私有频道 → 拉 bot 进频道并给管理权限 → 永久关闭该频道通知。
// 配置落盘由 internal/config 负责，挂载由 vfs 负责。

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"mytgfs/internal/brand"
	"mytgfs/internal/channel"
	"mytgfs/internal/cover"
	"mytgfs/internal/i18n"
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
	AvatarErr  error // 默认头像设置失败原因；非致命，卷仍可用
}

// Create 建卷：创建私有频道 → 拉 bot 进频道并提权 → 设默认头像 → 永久关闭通知。
//
// 输入：ctx；api 用 user 会话调用的 Telegram API；req 建卷参数
// 输出：Result；关通知/设头像失败不算致命，记在 Muted/MuteErr、AvatarErr 里
// 预期：建频道或拉 bot 失败直接返回 error；频道可能已经建出来，
//
//	调用方应把 channel id 提示给用户，便于手动清理
func Create(ctx context.Context, api channel.API, req Request) (Result, error) {
	req.Name = strings.TrimSpace(req.Name)
	req.Title = strings.TrimSpace(req.Title)
	req.BotUsername = strings.TrimPrefix(strings.TrimSpace(req.BotUsername), "@")
	if req.Name == "" {
		return Result{}, errors.New(i18n.T("err.volume_name_empty"))
	}
	if req.BotUsername == "" {
		return Result{}, errors.New(i18n.T("err.bot_username_empty"))
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
		// 拉 bot 失败（权限不足、用户名不存在、请求被取消）：频道刚建好还没进配置，
		// 留着就是用户既看不到也用不上的孤儿频道，统一删掉
		_ = CleanupCancelled(ctx, api, partial)
		return partial, fmt.Errorf(i18n.T("err.channel_created_bot_failed")+": %w", ch.ChannelID, err)
	}
	partial.BotID = botID

	// 默认头像用项目图标：新卷在 Telegram 里一眼能认出是 mytgfs
	if avatar, aerr := cover.SquareJPEG(brand.Large); aerr != nil {
		partial.AvatarErr = aerr
	} else if aerr := channel.SetPhoto(ctx, api, ch.ChannelID, ch.AccessHash, avatar); aerr != nil {
		partial.AvatarErr = aerr
	}

	if err := channel.MuteForever(ctx, api, ch); err != nil {
		partial.Muted = false
		partial.MuteErr = err
		if ctx.Err() != nil {
			_ = CleanupCancelled(ctx, api, partial)
			return partial, ctx.Err()
		}
		return partial, nil
	}
	partial.Muted = true
	// 设头像阶段被取消时同样不留孤儿频道
	if ctx.Err() != nil {
		_ = CleanupCancelled(ctx, api, partial)
		return partial, ctx.Err()
	}
	return partial, nil
}

// cleanupTimeout 是取消后清理孤儿频道的超时。
const cleanupTimeout = 15 * time.Second

// CleanupCancelled 删除建卷过程中已经建出来的频道。
//
// 输入：ctx 请求上下文（此时通常已取消）；api user 会话；res 建卷的部分结果
// 输出：删除错误；res 没有频道 id 时返回 nil
// 预期：用 context.WithoutCancel 保证请求取消后删除 RPC 仍能发完；失败只返回错误，由调用方记日志
func CleanupCancelled(ctx context.Context, api channel.DeleteAPI, res Result) error {
	if res.ChannelID == 0 {
		return nil
	}
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
	defer cancel()
	return channel.Delete(cctx, api, &channel.Result{
		ChannelID:  res.ChannelID,
		AccessHash: res.AccessHash,
	})
}

// ChatID 把 channel ID 转成 Bot API 风格的 -100... 形式。
//
// 输入：channelID 频道 id
// 输出：-1000000000000-channelID
// 预期：与 Bot API 的 chat_id 表示一致，仅用于展示与配置
func ChatID(channelID int64) int64 {
	return -1000000000000 - channelID
}
