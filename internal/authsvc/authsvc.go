package authsvc

// 面板登录服务：把凭证填写与 bot/user 登录拆成可单步调用的方法。
//
// CLI 的登录是交互式的（stdin 读验证码），网页没法用同一套流程，
// 所以这里把「发验证码」和「提交验证码」拆开：前端拿 phone_code_hash 再回调。
//
// 输入：Paths（配置与会话文件位置，零值走默认）
// 输出：Status（不含任何密钥内容）、Identity、单步错误
// 预期：每次调用临时开一条连接，用完即退；不会常驻后台

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tg"

	"mytgfs/internal/config"
	"mytgfs/internal/crypto"
	"mytgfs/internal/i18n"
	"mytgfs/internal/sessionstore"
	"mytgfs/internal/tgclient"
)

// 默认路径与 CLI 约定一致。
const (
	DefaultConfigPath   = "config.toml"
	DefaultAPIHashFile  = "secrets/api_hash"
	DefaultBotTokenFile = "secrets/bot_token"
	DefaultBotSession   = "secrets/bot.session"
	DefaultUserSession  = "secrets/user.session"
	DefaultKeyPath      = "secrets/session.key"
)

// Paths 是登录服务需要的文件位置。
type Paths struct {
	ConfigPath   string
	APIHashFile  string
	BotTokenFile string
	BotSession   string
	UserSession  string
	KeyPath      string
}

// Status 是凭证与登录态；只报「有没有」，不返回任何密钥内容。
type Status struct {
	APIID       int  `json:"api_id"`
	APIHashSet  bool `json:"api_hash_set"`
	BotTokenSet bool `json:"bot_token_set"`
	BotSession  bool `json:"bot_session"`
	UserSession bool `json:"user_session"`
}

// Identity 是登录后的账号身份。
type Identity struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Name     string `json:"name"`
	Bot      bool   `json:"bot"`
}

// CodeRequest 是发验证码的结果，前端把它原样带回下一步。
type CodeRequest struct {
	PhoneCodeHash string `json:"phone_code_hash"`
	Timeout       int    `json:"timeout"`
}

// FinishRequest 是提交验证码/2FA 的参数。
type FinishRequest struct {
	Phone         string `json:"phone"`
	Code          string `json:"code"`
	PhoneCodeHash string `json:"phone_code_hash"`
	Password      string `json:"password"`
}

// FinishResult 是登录结果；NeedPassword 为 true 时前端要再填 2FA 密码。
type FinishResult struct {
	NeedPassword bool     `json:"need_password"`
	User         Identity `json:"user"`
}

// ErrPhoneRequired 表示手机号为空。
type errPhoneRequired struct{}

func (errPhoneRequired) Error() string { return i18n.T("err.phone_required") }

// ErrPhoneRequired 手机号必填。
var ErrPhoneRequired error = errPhoneRequired{}

// ErrCodeRequired 表示验证码为空。
type errCodeRequired struct{}

func (errCodeRequired) Error() string { return i18n.T("err.code_required") }

// ErrCodeRequired 验证码必填。
var ErrCodeRequired error = errCodeRequired{}

// Service 是登录服务；无内部状态，可并发使用。
type Service struct{ p Paths }

// New 构造登录服务，空路径回落默认值。
func New(p Paths) *Service {
	return &Service{p: Paths{
		ConfigPath:   cmp.Or(p.ConfigPath, DefaultConfigPath),
		APIHashFile:  cmp.Or(p.APIHashFile, DefaultAPIHashFile),
		BotTokenFile: cmp.Or(p.BotTokenFile, DefaultBotTokenFile),
		BotSession:   cmp.Or(p.BotSession, DefaultBotSession),
		UserSession:  cmp.Or(p.UserSession, DefaultUserSession),
		KeyPath:      cmp.Or(p.KeyPath, DefaultKeyPath),
	}}
}

// Status 读取当前凭证与登录态。
//
// 输入：无
// 输出：Status；配置文件读不到时字段为零值
// 预期：只做本地文件检查，不连 Telegram
func (s *Service) Status() Status {
	var st Status
	cfg, err := config.Load(s.p.ConfigPath)
	if err == nil {
		st.APIID = cfg.Telegram.APIID
		st.APIHashSet = readTrim(cmp.Or(cfg.Telegram.APIHashFile, s.p.APIHashFile)) != ""
		st.BotTokenSet = readTrim(cmp.Or(cfg.Telegram.BotTokenFile, s.p.BotTokenFile)) != ""
	}
	st.BotSession = fileExists(s.p.BotSession)
	st.UserSession = fileExists(s.p.UserSession)
	return st
}

// SaveCredentials 写 api_id 与两个密钥文件。
//
// 输入：ctx；apiID（<=0 表示不改）；apiHash/botToken（空串表示不改）
// 输出：保存后的 Status
// 预期：api_hash 与 bot_token 以 0600 落盘；api_id 写回 [telegram] 段并保留注释
func (s *Service) SaveCredentials(ctx context.Context, apiID int, apiHash, botToken string) (Status, error) {
	_ = ctx
	if apiID > 0 {
		if err := config.SetTelegramAPIID(s.p.ConfigPath, apiID); err != nil {
			return Status{}, err
		}
	}
	cfg, _ := config.Load(s.p.ConfigPath)
	if v := strings.TrimSpace(apiHash); v != "" {
		if err := writeSecret(cmp.Or(cfg.Telegram.APIHashFile, s.p.APIHashFile), v); err != nil {
			return Status{}, err
		}
	}
	if v := strings.TrimSpace(botToken); v != "" {
		if err := writeSecret(cmp.Or(cfg.Telegram.BotTokenFile, s.p.BotTokenFile), v); err != nil {
			return Status{}, err
		}
	}
	return s.Status(), nil
}

// LoginBot 用 bot token 登录并返回 bot 身份。
//
// 输入：ctx；token（空串表示用已保存的 token）
// 输出：bot 身份；登录失败返回错误
// 预期：登录成功后才把 token 落盘，避免写坏已有 token
func (s *Service) LoginBot(ctx context.Context, token string) (Identity, error) {
	apiID, apiHash, cfg, err := s.apiParams()
	if err != nil {
		return Identity{}, err
	}
	token = cmp.Or(strings.TrimSpace(token), readTrim(cmp.Or(cfg.Telegram.BotTokenFile, s.p.BotTokenFile)))
	if token == "" {
		return Identity{}, errors.New(i18n.T("err.bot_token_required"))
	}
	key, err := crypto.LoadOrCreateKey(s.p.KeyPath)
	if err != nil {
		return Identity{}, fmt.Errorf("%s: %w", i18n.T("err.session_key_prefix"), err)
	}
	client := tgclient.New(apiID, apiHash,
		&sessionstore.Encrypted{Path: s.p.BotSession, Key: key}, tgclient.Options{})

	var idn Identity
	err = client.Run(ctx, func(ctx context.Context) error {
		if _, aerr := client.Auth().Bot(ctx, token); aerr != nil {
			return fmt.Errorf("%s: %w", i18n.T("err.bot_login_prefix"), aerr)
		}
		self, serr := client.Self(ctx)
		if serr != nil {
			return fmt.Errorf("%s: %w", i18n.T("err.self_prefix"), serr)
		}
		idn = identityOf(self, true)
		return nil
	})
	if err != nil {
		return Identity{}, err
	}
	if werr := writeSecret(cmp.Or(cfg.Telegram.BotTokenFile, s.p.BotTokenFile), token); werr != nil {
		return idn, werr
	}
	return idn, nil
}

// StartUserLogin 给手机号发登录验证码。
//
// 输入：ctx；phone 含国家码
// 输出：phone_code_hash 与超时秒数
// 预期：验证码在 Telegram 服务端有效，下一步换个连接也能用同一个 hash
func (s *Service) StartUserLogin(ctx context.Context, phone string) (CodeRequest, error) {
	phone = normalizePhone(phone)
	if phone == "" {
		return CodeRequest{}, ErrPhoneRequired
	}
	apiID, apiHash, _, err := s.apiParams()
	if err != nil {
		return CodeRequest{}, err
	}
	key, err := crypto.LoadOrCreateKey(s.p.KeyPath)
	if err != nil {
		return CodeRequest{}, fmt.Errorf("%s: %w", i18n.T("err.session_key_prefix"), err)
	}
	client := tgclient.New(apiID, apiHash,
		&sessionstore.Encrypted{Path: s.p.UserSession, Key: key}, tgclient.Options{})

	var out CodeRequest
	err = client.Run(ctx, func(ctx context.Context) error {
		sent, serr := client.Auth().SendCode(ctx, phone, auth.SendCodeOptions{})
		if serr != nil {
			return fmt.Errorf("%s: %w", i18n.T("err.user_login_prefix"), serr)
		}
		if v, ok := sent.(*tg.AuthSentCode); ok {
			out.PhoneCodeHash = v.PhoneCodeHash
			out.Timeout = v.Timeout
		}
		if out.PhoneCodeHash == "" {
			return errors.New(i18n.T("err.code_send_failed"))
		}
		return nil
	})
	if err != nil {
		return CodeRequest{}, err
	}
	return out, nil
}

// FinishUserLogin 提交验证码；需要 2FA 时先返回 NeedPassword，带密码再调一次。
//
// 输入：ctx；in 手机号、验证码、phone_code_hash、可选 2FA 密码
// 输出：NeedPassword 或已登录身份
// 预期：密码错误原样返回 Telegram 错误；成功后 user.session 已落盘
func (s *Service) FinishUserLogin(ctx context.Context, in FinishRequest) (FinishResult, error) {
	phone := normalizePhone(in.Phone)
	if phone == "" {
		return FinishResult{}, ErrPhoneRequired
	}
	if strings.TrimSpace(in.Code) == "" || strings.TrimSpace(in.PhoneCodeHash) == "" {
		return FinishResult{}, ErrCodeRequired
	}
	apiID, apiHash, _, err := s.apiParams()
	if err != nil {
		return FinishResult{}, err
	}
	key, err := crypto.LoadOrCreateKey(s.p.KeyPath)
	if err != nil {
		return FinishResult{}, fmt.Errorf("%s: %w", i18n.T("err.session_key_prefix"), err)
	}
	client := tgclient.New(apiID, apiHash,
		&sessionstore.Encrypted{Path: s.p.UserSession, Key: key}, tgclient.Options{})

	var out FinishResult
	err = client.Run(ctx, func(ctx context.Context) error {
		_, serr := client.Auth().SignIn(ctx, phone, strings.TrimSpace(in.Code), strings.TrimSpace(in.PhoneCodeHash))
		switch {
		case errors.Is(serr, auth.ErrPasswordAuthNeeded):
			if strings.TrimSpace(in.Password) == "" {
				out.NeedPassword = true
				return nil
			}
			if _, perr := client.Auth().Password(ctx, in.Password); perr != nil {
				return fmt.Errorf("%s: %w", i18n.T("err.user_login_prefix"), perr)
			}
		case serr != nil:
			return fmt.Errorf("%s: %w", i18n.T("err.user_login_prefix"), serr)
		}
		self, serr := client.Self(ctx)
		if serr != nil {
			return fmt.Errorf("%s: %w", i18n.T("err.self_prefix"), serr)
		}
		out.User = identityOf(self, false)
		return nil
	})
	if err != nil {
		return FinishResult{}, err
	}
	return out, nil
}

// Identity 读取已保存会话的身份。
//
// 输入：ctx；bot=true 读 bot 会话，否则读 user 会话
// 输出：身份；会话不存在或已失效时返回错误
// 预期：只读自身信息，不发消息
func (s *Service) Identity(ctx context.Context, bot bool) (Identity, error) {
	apiID, apiHash, _, err := s.apiParams()
	if err != nil {
		return Identity{}, err
	}
	path := s.p.UserSession
	if bot {
		path = s.p.BotSession
	}
	if !fileExists(path) {
		return Identity{}, errors.New(i18n.T("err.session_missing"))
	}
	key, err := crypto.LoadOrCreateKey(s.p.KeyPath)
	if err != nil {
		return Identity{}, fmt.Errorf("%s: %w", i18n.T("err.session_key_prefix"), err)
	}
	client := tgclient.New(apiID, apiHash,
		&sessionstore.Encrypted{Path: path, Key: key}, tgclient.Options{})

	var idn Identity
	err = client.Run(ctx, func(ctx context.Context) error {
		self, serr := client.Self(ctx)
		if serr != nil {
			if tgclient.IsAuthLost(serr) {
				return fmt.Errorf("%s: %w", i18n.T("err.auth_lost_hint"), serr)
			}
			return fmt.Errorf("%s: %w", i18n.T("err.self_prefix"), serr)
		}
		idn = identityOf(self, bot)
		return nil
	})
	if err != nil {
		return Identity{}, err
	}
	return idn, nil
}

// apiParams 读取 api_id/api_hash，缺失时在联网前失败。
func (s *Service) apiParams() (int, string, config.Config, error) {
	cfg, err := config.Load(s.p.ConfigPath)
	if err != nil {
		return 0, "", config.Config{}, err
	}
	apiID := cfg.Telegram.APIID
	apiHash := readTrim(cmp.Or(cfg.Telegram.APIHashFile, s.p.APIHashFile))
	if apiID <= 0 {
		return 0, "", cfg, errors.New(i18n.T("err.missing_api_id"))
	}
	if apiHash == "" {
		return 0, "", cfg, errors.New(i18n.T("err.missing_api_hash"))
	}
	return apiID, apiHash, cfg, nil
}

// identityOf 把 tg.User 转成精简身份。
func identityOf(u *tg.User, bot bool) Identity {
	name := strings.TrimSpace(u.FirstName + " " + u.LastName)
	if name == "" {
		name = u.Username
	}
	return Identity{ID: u.ID, Username: u.Username, Name: name, Bot: bot || u.Bot}
}

// normalizePhone 去掉空格、横线与括号，保留前导 +。
//
// 输入：用户输入的手机号
// 输出：Telegram 接受的紧凑格式；空串表示没填
func normalizePhone(phone string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(phone) {
		switch {
		case r >= '0' && r <= '9', r == '+':
			b.WriteRune(r)
		}
	}
	return b.String()
}

// writeSecret 以 0600 写密钥文件，父目录自动创建。
func writeSecret(path, value string) error {
	if err := os.MkdirAll(dirOf(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(value+"\n"), 0o600)
}

// dirOf 返回路径的目录部分；没有目录时返回 ".".
func dirOf(path string) string {
	if i := strings.LastIndexAny(path, `/\`); i >= 0 {
		return path[:i]
	}
	return "."
}

// readTrim 读文件并去空白；读不到返回空串。
func readTrim(path string) string {
	if path == "" {
		return ""
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// fileExists 判断普通文件是否存在。
func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Mode().IsRegular()
}
