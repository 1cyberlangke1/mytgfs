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

// LoginUser 用手机号登录（验证码从 stdin 读），成功后打印用户身份。
//
// 输入：ctx；p 参数；phone 含国家码；password 2FA 密码（可空）
// 输出：error
// 预期：若已登录则跳过；需要 2FA 且 password 为空时报错
func LoginUser(ctx context.Context, p Params, phone, password string) error {
	client := newClient(p)
	return client.Run(ctx, func(ctx context.Context) error {
		codeAuth := tgauth.CodeAuthenticatorFunc(func(ctx context.Context, sent *tg.AuthSentCode) (string, error) {
			fmt.Fprint(os.Stderr, i18n.T("prompt.code"))
			r := bufio.NewReader(os.Stdin)
			code, err := r.ReadString('\n')
			if err != nil {
				return "", err
			}
			return strings.TrimSpace(code), nil
		})
		flow := tgauth.NewFlow(tgauth.Constant(phone, password, codeAuth), tgauth.SendCodeOptions{})
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
