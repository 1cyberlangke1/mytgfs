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

	"mytgfs/internal/crypto"
	"mytgfs/internal/sessionstore"
	"mytgfs/internal/tgclient"
)

func TestSmokeUploadReadDelete(t *testing.T) {
	channelID, _ := strconv.ParseInt(os.Getenv("MYTGFS_TEST_CHANNEL"), 10, 64)
	if channelID == 0 {
		t.Skip("set MYTGFS_TEST_CHANNEL to run integration smoke test")
	}
	apiHash := readFile("../../secrets/api_hash")
	if apiHash == "" {
		t.Skip("missing ../../secrets/api_hash")
	}
	key, err := crypto.LoadOrCreateKey("../../secrets/session.key")
	if err != nil {
		t.Skipf("load session key: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	client := tgclient.New(0, apiHash,
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
		defer store.Delete(ctx, part.MessageID)

		offset := int64(512 << 10)
		got, err := store.ReadRange(ctx, part, offset, 1024)
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
