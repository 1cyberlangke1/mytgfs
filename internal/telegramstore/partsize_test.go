package telegramstore

import "testing"

// 回归背景：旧逻辑 128KB 起步、只有超过 4000 片才翻倍，
// 中等文件（如 100MB）要发 782 个 RPC 而不是 196 个，白白慢 4 倍。
// FastTelethon / 桌面客户端等第三方实现都固定用协议上限 512KB。
func TestUploadPartSizeAlwaysMax(t *testing.T) {
	for _, size := range []int64{
		1, 512 * 1024, 10 << 20, 100 << 20, 512 << 20, MaxFileSize,
	} {
		if got := uploadPartSizeFor(size); got != uploadMaxPartSize {
			t.Fatalf("uploadPartSizeFor(%d) = %d; want 恒为 %d", size, got, uploadMaxPartSize)
		}
	}
}

// 512KB 分片必须能装下最大文件：4000 片恰好覆盖 2GB，不需要为任何尺寸缩小分片
func TestMaxFileSizeFitsMaxPartSize(t *testing.T) {
	if total := partsOf(MaxFileSize, uploadMaxPartSize); total > uploadMaxParts {
		t.Fatalf("MaxFileSize 需 %d 片，超过协议上限 %d", total, uploadMaxParts)
	}
}
