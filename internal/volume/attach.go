package volume

// 把一个已经存在的频道挂成卷。
//
// 与 Create 的区别：不建频道、不拉 bot，只校验「bot 能看到这个频道且有发言权」，
// 然后由调用方把结果写进配置。频道可以先在 Telegram 里手工建好并加 bot 为管理员。

import (
	"context"
	"errors"
	"strings"

	"mytgfs/internal/channel"
	"mytgfs/internal/i18n"
)

// AttachRequest 是挂载已有频道的参数。
type AttachRequest struct {
	Name   string // 卷名，用于 index-<name>.db 与配置
	ChatID int64  // Bot API 形式 -100... 或原始频道 id
}

// Attach 校验已有频道可用性并返回可写入配置的卷信息。
//
// 输入：ctx；api bot 会话；req 卷名与频道 id
// 输出：Result；频道不可见或 bot 无发言权时返回 error
// 预期：只读 RPC，不向频道发消息；chat_id 支持 -100 前缀与裸 id
func Attach(ctx context.Context, api channel.CheckAPI, req AttachRequest) (Result, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return Result{}, errors.New(i18n.T("err.volume_name_empty"))
	}
	channelID := NormalizeChatID(req.ChatID)
	if channelID == 0 {
		return Result{}, errors.New(i18n.T("err.volume_chat_id_invalid"))
	}
	h, err := channel.Check(ctx, api, channelID)
	if err != nil {
		return Result{}, err
	}
	return Result{
		Name:      name,
		ChannelID: channelID,
		ChatID:    ChatID(channelID),
		Title:     h.Title,
	}, nil
}

// NormalizeChatID 把 -100... 或裸 id 统一成频道 id。
//
// 输入：id Bot API 形式或原始 id
// 输出：频道 id；无法识别返回 0
// 预期：-100 前缀按 -1000000000000 - channelID 反算
func NormalizeChatID(id int64) int64 {
	switch {
	case id < -1000000000000:
		return -id - 1000000000000
	case id > 0:
		return id
	default:
		return 0
	}
}
