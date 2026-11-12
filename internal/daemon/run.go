package daemon

// daemon 前台运行：持有一条 bot 连接，提供 IPC 控制。
//
// 崩溃边界：
//   - daemon.json 原子写，断电只会是旧状态或完整新状态
//   - 退出时删除 daemon.json

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"

	"mytgfs/internal/config"
	"mytgfs/internal/crypto"
	"mytgfs/internal/datadir"
	"mytgfs/internal/index"
	"mytgfs/internal/ipc"
	"mytgfs/internal/sessionstore"
	"mytgfs/internal/telegramstore"
	"mytgfs/internal/tgclient"
)

// Run 前台运行 daemon，直到 ctx 取消或收到 stop。
func Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	cfg, err := config.Load("config.toml")
	if err != nil {
		return fmt.Errorf("读取配置: %w", err)
	}
	apiHash := cfg.APIHash()
	if apiHash == "" {
		return fmt.Errorf("缺少 api_hash")
	}
	key, err := crypto.LoadOrCreateKey("secrets/session.key")
	if err != nil {
		return err
	}
	idx, err := index.Open(datadir.Path("index.db"))
	if err != nil {
		return err
	}
	defer idx.Close()

	token, err := randomToken()
	if err != nil {
		return err
	}
	srv, err := ipc.Listen(token)
	if err != nil {
		return err
	}
	defer srv.Close()

	if err := writeState(State{Addr: srv.Addr(), Token: token, PID: os.Getpid()}); err != nil {
		return err
	}
	defer removeState()

	client := tgclient.New(cfg.Telegram.APIID, apiHash,
		&sessionstore.Encrypted{Path: "secrets/bot.session", Key: key},
		tgclient.Options{})

	return client.Run(ctx, func(ctx context.Context) error {
		_ = telegramstore.New(client.API(), cfg.Telegram.ChannelID)

		srv.SetHandler(func(ctx context.Context, action string, data json.RawMessage) (any, error) {
			switch action {
			case "status":
				entries, err := idx.List(ctx, "/")
				if err != nil {
					return nil, err
				}
				return map[string]any{
					"addr":       srv.Addr(),
					"pid":        os.Getpid(),
					"channel_id": cfg.Telegram.ChannelID,
					"entries":    len(entries),
				}, nil
			case "stop":
				cancel()
				return map[string]any{"stopped": true}, nil
			default:
				return nil, fmt.Errorf("未知 action: %s", action)
			}
		})
		go func() { _ = srv.Serve(ctx) }()
		<-ctx.Done()
		return nil
	})
}

// Start 后台启动 daemon（detached）。
func Start(exe string) error {
	logFile, err := os.OpenFile(datadir.Path("daemon.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer logFile.Close()

	cmd := newRunCommand(exe)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	detached(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// randomToken 生成 16 字节随机 token。
func randomToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
