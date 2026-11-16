package app

// 频道 access_hash 的本地持久化。
//
// 每次进程启动第一次用到频道时，telegramstore 都要调一次 channels.getChannels
// 才能拿到 access_hash（约 1 个 RTT）。把 access_hash 存进卷的本地索引后，
// 上传/改名这些只需要 InputChannel 的操作就能直接开跑。
//
// 只缓存 access_hash，不缓存标题：标题会随频道更新变化，必须按需回源。

import (
	"context"
	"strconv"

	"mytgfs/internal/index"
)

// channelCache 用卷的本地索引 meta 表存 access_hash。
type channelCache struct{ idx *index.Index }

// channelCacheKey 返回某个频道的缓存键。
//
// 输入：chatID 频道号
// 输出：meta 表键名
// 预期：按频道号隔离，换频道后旧键不会命中
func channelCacheKey(chatID int64) string {
	return "channel_hash:" + strconv.FormatInt(chatID, 10)
}

// LoadAccessHash 读缓存的 access_hash。
//
// 输入：ctx；chatID
// 输出：access_hash、是否命中；读失败按未命中处理
// 预期：idx 为空时恒未命中，调用方回落到 RPC
func (c channelCache) LoadAccessHash(ctx context.Context, chatID int64) (int64, bool, error) {
	if c.idx == nil {
		return 0, false, nil
	}
	v, ok, err := c.idx.GetMeta(ctx, channelCacheKey(chatID))
	if err != nil || !ok || v == 0 {
		return 0, false, err
	}
	return v, true, nil
}

// SaveAccessHash 写缓存。
//
// 输入：ctx；chatID；accessHash
// 输出：error
// 预期：accessHash 为 0 或没有索引时静默跳过
func (c channelCache) SaveAccessHash(ctx context.Context, chatID, accessHash int64) error {
	if c.idx == nil || accessHash == 0 {
		return nil
	}
	return c.idx.PutMeta(ctx, channelCacheKey(chatID), accessHash)
}

// DropAccessHash 删缓存。
//
// 输入：ctx；chatID
// 输出：error
// 预期：频道迁移、access_hash 失效或换频道时调用，下次重新解析
func (c channelCache) DropAccessHash(ctx context.Context, chatID int64) error {
	if c.idx == nil {
		return nil
	}
	return c.idx.DeleteMeta(ctx, channelCacheKey(chatID))
}
