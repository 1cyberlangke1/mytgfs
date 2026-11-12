//go:build integration

package telegramstore

// 真实 Telegram 冒烟测试：超长 caption 独立消息。
//
// 跑法：
//   MYTGFS_TEST_CHANNEL=<channel_id> go test -tags=integration ./internal/telegramstore -run TestSmokeLongCaption -v

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gotd/td/tg"

	"mytgfs/internal/crypto"
	"mytgfs/internal/sessionstore"
	"mytgfs/internal/tgclient"
)

func TestSmokeLongCaption(t *testing.T) {
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

	longCaption := `{"path":"/` + strings.Repeat("deep/", 200) + `file.txt"}`

	err = client.Run(ctx, func(ctx context.Context) error {
		store := New(client.API(), channelID)
		ch, err := store.resolve(ctx)
		if err != nil {
			return err
		}

		part, err := store.Upload(ctx, bytes.NewReader([]byte("x")), 1,
			"long.txt", "text/plain", longCaption)
		if err != nil {
			return err
		}

		msg, err := getMessage(ctx, client.API(), ch, part.MessageID)
		if err != nil {
			return err
		}
		if !strings.HasPrefix(msg.Message, `{"ref":`) {
			t.Fatalf("caption should be ref, got %q", msg.Message)
		}

		var ref struct {
			Ref int `json:"ref"`
		}
		if err := json.Unmarshal([]byte(msg.Message), &ref); err != nil {
			t.Fatalf("parse ref: %v", err)
		}

		meta, err := getMessage(ctx, client.API(), ch, ref.Ref)
		if err != nil {
			return err
		}
		if meta.Message != longCaption {
			t.Fatalf("meta mismatch: got len=%d want len=%d", len(meta.Message), len(longCaption))
		}

		fmt.Printf("REF=%d META=%d meta_len=%d\n", part.MessageID, ref.Ref, len(meta.Message))
		if os.Getenv("MYTGFS_KEEP") != "" {
			fmt.Println("KEEP: 已保留消息")
			return nil
		}
		return store.Delete(ctx, part.MessageID, ref.Ref)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func getMessage(ctx context.Context, api *tg.Client, ch *tg.Channel, id int) (*tg.Message, error) {
	res, err := api.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{
		Channel: &tg.InputChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash},
		ID:      []tg.InputMessageClass{&tg.InputMessageID{ID: id}},
	})
	if err != nil {
		return nil, err
	}
	var msgs []tg.MessageClass
	switch m := res.(type) {
	case *tg.MessagesMessages:
		msgs = m.Messages
	case *tg.MessagesMessagesSlice:
		msgs = m.Messages
	case *tg.MessagesChannelMessages:
		msgs = m.Messages
	}
	for _, mc := range msgs {
		if msg, ok := mc.(*tg.Message); ok {
			return msg, nil
		}
	}
	return nil, ErrNoDocument
}
