package telegramstore

// 可后置接线的 RPC 代理：让卷在 Telegram 建连之前就能用本地索引挂起来。
//
// 卷的索引、队列、目录结构全在本地 SQLite，不需要网络；只有真正读写频道
// 内容时才需要已认证客户端。Store 在打开卷时就要一个 API，这里先给一个
// 空壳，等连接循环拿到真实客户端再 Set 进来，Store 与 downloader 不必重建。

import (
	"context"
	"sync"

	"github.com/gotd/td/tg"
)

// APIProxy 是把「真实客户端」推迟到运行期再接线的 API 实现。
//
// 输入：Set 传入已认证的 API；未接线或 Set(nil) 视为离线
// 输出：每个 RPC 未接线时返回 ErrOffline，接线后原样转发
// 预期：Store 与它的 downloader 共用同一个代理，Set 对在飞与后续请求同时生效
type APIProxy struct {
	mu  sync.RWMutex
	api API
}

// NewAPIProxy 创建未接线的代理。
func NewAPIProxy() *APIProxy { return &APIProxy{} }

// Set 接线到真实 API；传 nil 表示断开（后续请求回到 ErrOffline）。
func (p *APIProxy) Set(api API) {
	p.mu.Lock()
	p.api = api
	p.mu.Unlock()
}

// Offline 报告当前是否还没接线。
func (p *APIProxy) Offline() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.api == nil
}

// target 取当前转发目标。
//
// 输入：无
// 输出：已接线的 API；未接线返回 ErrOffline
// 预期：锁内只读指针，返回值在锁外使用
func (p *APIProxy) target() (API, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.api == nil {
		return nil, ErrOffline
	}
	return p.api, nil
}

// ChannelsGetChannels 转发 channels.getChannels。
func (p *APIProxy) ChannelsGetChannels(ctx context.Context, id []tg.InputChannelClass) (tg.MessagesChatsClass, error) {
	api, err := p.target()
	if err != nil {
		return nil, err
	}
	return api.ChannelsGetChannels(ctx, id)
}

// ChannelsGetMessages 转发 channels.getMessages。
func (p *APIProxy) ChannelsGetMessages(ctx context.Context, request *tg.ChannelsGetMessagesRequest) (tg.MessagesMessagesClass, error) {
	api, err := p.target()
	if err != nil {
		return nil, err
	}
	return api.ChannelsGetMessages(ctx, request)
}

// ChannelsEditTitle 转发 channels.editTitle。
func (p *APIProxy) ChannelsEditTitle(ctx context.Context, request *tg.ChannelsEditTitleRequest) (tg.UpdatesClass, error) {
	api, err := p.target()
	if err != nil {
		return nil, err
	}
	return api.ChannelsEditTitle(ctx, request)
}

// UsersGetUsers 转发 users.getUsers。
func (p *APIProxy) UsersGetUsers(ctx context.Context, request []tg.InputUserClass) ([]tg.UserClass, error) {
	api, err := p.target()
	if err != nil {
		return nil, err
	}
	return api.UsersGetUsers(ctx, request)
}

// MessagesSendMedia 转发 messages.sendMedia。
func (p *APIProxy) MessagesSendMedia(ctx context.Context, request *tg.MessagesSendMediaRequest) (tg.UpdatesClass, error) {
	api, err := p.target()
	if err != nil {
		return nil, err
	}
	return api.MessagesSendMedia(ctx, request)
}

// MessagesSendMessage 转发 messages.sendMessage。
func (p *APIProxy) MessagesSendMessage(ctx context.Context, request *tg.MessagesSendMessageRequest) (tg.UpdatesClass, error) {
	api, err := p.target()
	if err != nil {
		return nil, err
	}
	return api.MessagesSendMessage(ctx, request)
}

// MessagesEditMessage 转发 messages.editMessage。
func (p *APIProxy) MessagesEditMessage(ctx context.Context, request *tg.MessagesEditMessageRequest) (tg.UpdatesClass, error) {
	api, err := p.target()
	if err != nil {
		return nil, err
	}
	return api.MessagesEditMessage(ctx, request)
}

// ChannelsGetFullChannel 转发 channels.getFullChannel。
func (p *APIProxy) ChannelsGetFullChannel(ctx context.Context, channel tg.InputChannelClass) (*tg.MessagesChatFull, error) {
	api, err := p.target()
	if err != nil {
		return nil, err
	}
	return api.ChannelsGetFullChannel(ctx, channel)
}

// UploadGetFile 转发 upload.getFile。
func (p *APIProxy) UploadGetFile(ctx context.Context, request *tg.UploadGetFileRequest) (tg.UploadFileClass, error) {
	api, err := p.target()
	if err != nil {
		return nil, err
	}
	return api.UploadGetFile(ctx, request)
}

// UploadGetFileHashes 转发 upload.getFileHashes（downloader 用）。
func (p *APIProxy) UploadGetFileHashes(ctx context.Context, request *tg.UploadGetFileHashesRequest) ([]tg.FileHash, error) {
	api, err := p.target()
	if err != nil {
		return nil, err
	}
	return api.UploadGetFileHashes(ctx, request)
}

// UploadReuploadCDNFile 转发 upload.reuploadCDNFile（downloader 用）。
func (p *APIProxy) UploadReuploadCDNFile(ctx context.Context, request *tg.UploadReuploadCDNFileRequest) ([]tg.FileHash, error) {
	api, err := p.target()
	if err != nil {
		return nil, err
	}
	return api.UploadReuploadCDNFile(ctx, request)
}

// UploadGetCDNFileHashes 转发 upload.getCDNFileHashes（downloader 用）。
func (p *APIProxy) UploadGetCDNFileHashes(ctx context.Context, request *tg.UploadGetCDNFileHashesRequest) ([]tg.FileHash, error) {
	api, err := p.target()
	if err != nil {
		return nil, err
	}
	return api.UploadGetCDNFileHashes(ctx, request)
}

// UploadGetWebFile 转发 upload.getWebFile（downloader 用）。
func (p *APIProxy) UploadGetWebFile(ctx context.Context, request *tg.UploadGetWebFileRequest) (*tg.UploadWebFile, error) {
	api, err := p.target()
	if err != nil {
		return nil, err
	}
	return api.UploadGetWebFile(ctx, request)
}

// UploadSaveFilePart 转发 upload.saveFilePart。
func (p *APIProxy) UploadSaveFilePart(ctx context.Context, request *tg.UploadSaveFilePartRequest) (bool, error) {
	api, err := p.target()
	if err != nil {
		return false, err
	}
	return api.UploadSaveFilePart(ctx, request)
}

// UploadSaveBigFilePart 转发 upload.saveBigFilePart。
func (p *APIProxy) UploadSaveBigFilePart(ctx context.Context, request *tg.UploadSaveBigFilePartRequest) (bool, error) {
	api, err := p.target()
	if err != nil {
		return false, err
	}
	return api.UploadSaveBigFilePart(ctx, request)
}
