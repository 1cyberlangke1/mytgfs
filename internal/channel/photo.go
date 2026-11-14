package channel

// 频道头像写入：把一张已归一的 JPEG 上传成频道头像。
//
// upload.saveFilePart 单块上限 512 KiB，而 512×512 JPEG 远小于该值，
// 所以这里只发 0 号分片，不做分块续传。

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"

	"github.com/gotd/td/tg"

	"mytgfs/internal/i18n"
)

// maxPhotoBytes 是单块上传能带的字节上限（512 KiB）。
const maxPhotoBytes = 512 << 10

// PhotoAPI 是写入头像需要的最小接口。
//
// 输入/输出：与 tg.Client 同名方法一致
// 预期：channel.API 与 telegramstore.API 都是它的超集，可直接传入
type PhotoAPI interface {
	ChannelsGetChannels(ctx context.Context, req []tg.InputChannelClass) (tg.MessagesChatsClass, error)
	ChannelsEditPhoto(ctx context.Context, req *tg.ChannelsEditPhotoRequest) (tg.UpdatesClass, error)
	UploadSaveFilePart(ctx context.Context, req *tg.UploadSaveFilePartRequest) (bool, error)
}

// SetPhoto 把 JPEG 字节设置成频道头像。
//
// 输入：ctx；api 有 canChangeInfo 权限的会话（user 或管理员 bot）；
//
//	channelID 频道 id；accessHash 频道访问哈希，0 表示需要现解析；data 头像 JPEG
//
// 输出：error；成功即 Telegram 侧头像已更新
// 预期：
//   - accessHash 为 0 时先 channels.getChannels 解析，避免调 editPhoto 时被拒
//   - 上传失败、被服务端拒绝、超过单块上限都要返回明确错误
func SetPhoto(ctx context.Context, api PhotoAPI, channelID, accessHash int64, data []byte) error {
	if len(data) == 0 {
		return fmt.Errorf("%s", i18n.T("err.cover_empty"))
	}
	if len(data) > maxPhotoBytes {
		return fmt.Errorf("%s", i18n.T("err.cover_set"))
	}
	if accessHash == 0 {
		resolved, err := resolveAccessHash(ctx, api, channelID)
		if err != nil {
			return err
		}
		accessHash = resolved
	}

	fileID, err := randomFileID()
	if err != nil {
		return err
	}
	ok, err := api.UploadSaveFilePart(ctx, &tg.UploadSaveFilePartRequest{
		FileID:   fileID,
		FilePart: 0,
		Bytes:    data,
	})
	if err != nil {
		return fmt.Errorf("%s: %w", i18n.T("err.cover_set"), err)
	}
	if !ok {
		return fmt.Errorf("%s", i18n.T("err.cover_set"))
	}

	_, err = api.ChannelsEditPhoto(ctx, &tg.ChannelsEditPhotoRequest{
		Channel: &tg.InputChannel{ChannelID: channelID, AccessHash: accessHash},
		Photo: &tg.InputChatUploadedPhoto{
			File: &tg.InputFile{ID: fileID, Parts: 1, Name: "avatar.jpg"},
		},
	})
	if err != nil {
		return fmt.Errorf("%s: %w", i18n.T("err.cover_set"), err)
	}
	return nil
}

// resolveAccessHash 用 channels.getChannels 取真实 access_hash。
//
// 输入：ctx；api；channelID
// 输出：access_hash；频道不可见时报错
// 预期：access_hash 传 0 也能解析出真实值（bot 已验证可用）
func resolveAccessHash(ctx context.Context, api PhotoAPI, channelID int64) (int64, error) {
	res, err := api.ChannelsGetChannels(ctx, []tg.InputChannelClass{
		&tg.InputChannel{ChannelID: channelID},
	})
	if err != nil {
		return 0, fmt.Errorf("%s: %w", i18n.T("err.cover_set"), err)
	}
	var chats []tg.ChatClass
	switch m := res.(type) {
	case *tg.MessagesChats:
		chats = m.Chats
	case *tg.MessagesChatsSlice:
		chats = m.Chats
	}
	for _, c := range chats {
		if ch, ok := c.(*tg.Channel); ok && ch.ID == channelID {
			return ch.AccessHash, nil
		}
	}
	return 0, fmt.Errorf("%s", i18n.T("err.channel_not_visible", channelID))
}

// randomFileID 生成上传用的随机文件 id。
func randomFileID() (int64, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 0, err
	}
	id := binary.LittleEndian.Uint64(b[:]) >> 1
	return int64(id), nil
}
