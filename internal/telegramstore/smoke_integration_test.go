//go:build integration

package telegramstore

// 真实 Telegram 冒烟测试。
//
// 默认不编译（需要 -tags=integration），跑之前必须设置：
//   MYTGFS_TEST_CHANNEL=<channel_id>
// 并确保 ../../secrets/ 下已有 api_hash、session.key、bot.session。

import (
	"bytes"
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/gotd/td/tg"

	"mytgfs/internal/crypto"
	"mytgfs/internal/sessionstore"
	"mytgfs/internal/tgclient"
)

// deleteTestMessages 直调底层 API 删除测试消息；生产代码已无批量删除入口，
// 清理能力只保留在集成测试里。
//
// 输入：ctx；s 用于解析频道 access_hash；api 真实 Telegram 客户端；ids 消息号
// 输出：error
// 预期：空列表不发请求
func deleteTestMessages(ctx context.Context, s *Store, api *tg.Client, ids ...int) error {
	if len(ids) == 0 {
		return nil
	}
	ch, err := s.resolve(ctx)
	if err != nil {
		return err
	}
	_, err = api.ChannelsDeleteMessages(ctx, &tg.ChannelsDeleteMessagesRequest{
		Channel: &tg.InputChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash},
		ID:      ids,
	})
	return err
}

func TestSmokeUploadReadDelete(t *testing.T) {
	channelID, _ := strconv.ParseInt(os.Getenv("MYTGFS_TEST_CHANNEL"), 10, 64)
	if channelID == 0 {
		t.Skip("set MYTGFS_TEST_CHANNEL to run integration smoke test")
	}
	apiHash := readFile("../../secrets/api_hash")
	if apiHash == "" {
		t.Skip("missing ../../secrets/api_hash")
	}
	// api_id 属于应用所有者私有信息，不进源码；测试时用环境变量传入
	apiID, _ := strconv.Atoi(os.Getenv("MYTGFS_API_ID"))
	if apiID == 0 {
		t.Skip("set MYTGFS_API_ID to run integration smoke test")
	}
	key, err := crypto.LoadOrCreateKey("../../secrets/session.key")
	if err != nil {
		t.Skipf("load session key: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	client := tgclient.New(apiID, apiHash,
		&sessionstore.Encrypted{Path: "../../secrets/bot.session", Key: key},
		tgclient.Options{})

	err = client.Run(ctx, func(ctx context.Context) error {
		store := New(client.API(), channelID)

		data := make([]byte, 1<<20)
		for i := range data {
			data[i] = byte(i)
		}
		part, err := store.Upload(ctx, bytes.NewReader(data), int64(len(data)),
			"smoke.bin", "application/octet-stream", `{"path":"/smoke.bin"}`)
		if err != nil {
			return err
		}
		defer deleteTestMessages(ctx, store, client.API(), part.MessageID)

		offset := int64(512 << 10)
		got, err := store.ReadRange(ctx, part, offset, 1024, nil)
		if err != nil {
			return err
		}
		if !bytes.Equal(got, data[offset:offset+1024]) {
			t.Fatal("range mismatch")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func readFile(p string) string {
	b, _ := os.ReadFile(p)
	return string(bytes.TrimSpace(b))
}
