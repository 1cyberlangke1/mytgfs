package main

// CLI 侧临时会话的统一入口。
//
// 原来 channel / volume / web / auth 命令各自抄一遍
// 「读凭据 → 开密钥 → 建 client → 开可取消 ctx → Run」，
// 会话文件路径还散落成字符串字面量。这里收成两件事：
//   - mustSessionKey：读或创建会话加密密钥
//   - runSession / runUserSession：开一条临时连接执行 fn

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/gotd/td/tg"

	"mytgfs/internal/app"
	"mytgfs/internal/crypto"
	"mytgfs/internal/i18n"
	"mytgfs/internal/sessionstore"
	"mytgfs/internal/tgclient"
)

// mustSessionKey 读取或创建会话加密密钥。
//
// 输入：无（路径固定为 app.DefaultKeyPath）
// 输出：32 字节密钥；读不出来时打印人话并退出 1
func mustSessionKey() []byte {
	key, err := crypto.LoadOrCreateKey(app.DefaultKeyPath)
	if err != nil {
		fatal(fmt.Sprintf("%s: %v", i18n.T("err.session_key_prefix"), err))
	}
	return key
}

// interruptContext 返回一个 Ctrl+C 可取消的 ctx。
//
// 输入：无
// 输出：ctx 与 cancel；调用方负责 defer cancel()
func interruptContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt)
}

// runSession 打开 path 指向的会话执行 fn。
//
// 输入：path 会话文件；opts 连接选项；fn 连接就绪后的回调（拿到已登录 API）
// 输出：fn 的业务错误优先返回，其次才是连接错误
// 预期：
//   - 一次性命令使用，fn 返回后连接立即关闭
//   - 业务错误不被 gotd 包成连接错误
func runSession(path string, opts tgclient.Options, fn func(ctx context.Context, api *tg.Client) error) error {
	apiID, apiHash := loadAPI()
	client := tgclient.New(apiID, apiHash,
		&sessionstore.Encrypted{Path: path, Key: mustSessionKey()}, opts)

	ctx, cancel := interruptContext()
	defer cancel()

	var fnErr error
	runErr := client.Run(ctx, func(ctx context.Context) error {
		fnErr = fn(ctx, client.API())
		// 返回 nil 让 gotd 收尾退出，避免把业务错误包成连接错误
		return nil
	})
	if fnErr != nil {
		return fnErr
	}
	return runErr
}

// runUserSession 用 user 会话执行 fn。
//
// 输入：fn 连接就绪后的回调
// 输出：同 runSession
// 预期：建频道、列频道、加 bot 这类 bot 会话做不到的操作使用
func runUserSession(fn func(ctx context.Context, api *tg.Client) error) error {
	return runSession(app.DefaultUserSessionPath, tgclient.Options{}, fn)
}
