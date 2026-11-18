package auth

// Telegram 登录：bot（token）与 user（手机号 + 验证码 + 可选 2FA）。

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/gotd/td/telegram"
	tgauth "github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tg"

	"mytgfs/internal/i18n"
	"mytgfs/internal/sessionstore"
	"mytgfs/internal/tgclient"
)

// Params 是登录所需的固定参数。
type Params struct {
	APIID   int
	APIHash string
	Session *sessionstore.Encrypted
}

func newClient(p Params) *tgclient.Client {
	return tgclient.New(p.APIID, p.APIHash, p.Session, tgclient.Options{})
}

// LoginBot 用 bot token 登录，成功后打印 bot 身份。
//
// 输入：ctx；p 参数；token bot token
// 输出：error
// 预期：session 由 p.Session 加密落盘
func LoginBot(ctx context.Context, p Params, token string) error {
	client := newClient(p)
	return client.Run(ctx, func(ctx context.Context) error {
		if _, err := client.Auth().Bot(ctx, token); err != nil {
			return fmt.Errorf("%s: %w", i18n.T("err.bot_login_prefix"), err)
		}
		return printSelf(ctx, client.Client)
	})
}

// codeReader 返回从 stdin 读验证码的回调。
//
// 输入：stdin 已包好的缓冲读取器
// 输出：gotd 的验证码回调
// 预期：整个登录流程只包一次 bufio；重试验证码时不会丢掉上一次已缓冲的
// 后续行（管道/脚本一次喂多行时直接卡住）
func codeReader(stdin *bufio.Reader) tgauth.CodeAuthenticatorFunc {
	return func(ctx context.Context, sent *tg.AuthSentCode) (string, error) {
		fmt.Fprint(os.Stderr, i18n.T("prompt.code"))
		code, err := stdin.ReadString('\n')
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(code), nil
	}
}

// LoginUser 用手机号登录（验证码从 stdin 读），成功后打印用户身份。
//
// 输入：ctx；p 参数；phone 含国家码；password 2FA 密码（可空）
// 输出：error
// 预期：若已登录则跳过；需要 2FA 且 password 为空时报错
func LoginUser(ctx context.Context, p Params, phone, password string) error {
	client := newClient(p)
	return client.Run(ctx, func(ctx context.Context) error {
		// stdin 只包一次 bufio：每次验证码重试都新建 Reader 会丢掉
		// 上一次已缓冲的后续行（管道/脚本一次喂多行时直接卡住）
		stdin := bufio.NewReader(os.Stdin)
		flow := tgauth.NewFlow(tgauth.Constant(phone, password, codeReader(stdin)), tgauth.SendCodeOptions{})
		if err := client.Auth().IfNecessary(ctx, flow); err != nil {
			return fmt.Errorf("%s: %w", i18n.T("err.user_login_prefix"), err)
		}
		return printSelf(ctx, client.Client)
	})
}

// printSelf 读取并打印当前登录身份。
func printSelf(ctx context.Context, client *telegram.Client) error {
	self, err := client.Self(ctx)
	if err != nil {
		return fmt.Errorf("%s: %w", i18n.T("err.self_prefix"), err)
	}
	fmt.Println(i18n.T("ok.login", self.ID, self.Username))
	return nil
}

// Username 读取当前会话的用户名（不带 @）。
//
// 输入：ctx；client 运行中的客户端
// 输出：用户名；没有用户名时返回 error
// 预期：用于从 bot 会话自动拿到 bot 用户名，省去手工输入
func Username(ctx context.Context, client *telegram.Client) (string, error) {
	self, err := client.Self(ctx)
	if err != nil {
		return "", fmt.Errorf("%s: %w", i18n.T("err.self_prefix"), err)
	}
	if self.Username == "" {
		return "", fmt.Errorf("%s", i18n.T("err.bot_no_username"))
	}
	return self.Username, nil
}
