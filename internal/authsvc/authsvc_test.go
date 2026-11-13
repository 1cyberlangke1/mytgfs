package authsvc

// 登录服务的本地行为测试：只覆盖不联网的分支（状态、落盘、参数校验）。

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// newService 造一个指向临时目录的服务，配置里带 api_id。
//
// 输入：t；apiID 写入 config.toml；apiHash/token 非空时写入对应密钥文件
// 输出：Service 与临时目录路径
func newService(t *testing.T, apiID int, apiHash, token string) (*Service, string) {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	cfg := "# 保留注释\n[telegram]\napi_id = " + itoa(apiID) + "\napi_hash_file = \"" +
		filepath.ToSlash(filepath.Join(dir, "secrets", "api_hash")) + "\"\n"
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	if apiHash != "" {
		if err := writeSecret(filepath.Join(dir, "secrets", "api_hash"), apiHash); err != nil {
			t.Fatal(err)
		}
	}
	if token != "" {
		if err := writeSecret(filepath.Join(dir, "secrets", "bot_token"), token); err != nil {
			t.Fatal(err)
		}
	}
	return New(Paths{
		ConfigPath:   cfgPath,
		APIHashFile:  filepath.Join(dir, "secrets", "api_hash"),
		BotTokenFile: filepath.Join(dir, "secrets", "bot_token"),
		BotSession:   filepath.Join(dir, "secrets", "bot.session"),
		UserSession:  filepath.Join(dir, "secrets", "user.session"),
		KeyPath:      filepath.Join(dir, "secrets", "session.key"),
	}), dir
}

func itoa(n int) string { return strconv.Itoa(n) }

// TestStatusReportsPresenceOnly 状态只报「有没有」，不回传密钥内容。
//
// 输入：api_id + api_hash + token 都已落盘
// 输出：三个 set 标记为 true，会话标记为 false
// 预期：Status 结构体里没有密钥字段
func TestStatusReportsPresenceOnly(t *testing.T) {
	s, _ := newService(t, 12345, "hash-value", "token-value")
	st := s.Status()
	if st.APIID != 12345 || !st.APIHashSet || !st.BotTokenSet {
		t.Fatalf("status = %+v", st)
	}
	if st.BotSession || st.UserSession {
		t.Fatalf("未登录时会话标记应为 false: %+v", st)
	}
}

// TestSaveCredentialsWritesFiles 保存后状态与文件内容都要更新。
func TestSaveCredentialsWritesFiles(t *testing.T) {
	s, dir := newService(t, 1, "", "")
	st, err := s.SaveCredentials(context.Background(), 777, "new-hash", "new-token")
	if err != nil {
		t.Fatal(err)
	}
	if st.APIID != 777 || !st.APIHashSet || !st.BotTokenSet {
		t.Fatalf("status = %+v", st)
	}
	hash, err := os.ReadFile(filepath.Join(dir, "secrets", "api_hash"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(hash)) != "new-hash" {
		t.Fatalf("api_hash = %q", hash)
	}
}

// TestSaveCredentialsZeroKeepsExisting 空值表示不修改，不能把已有凭证擦掉。
func TestSaveCredentialsZeroKeepsExisting(t *testing.T) {
	s, _ := newService(t, 42, "keep-hash", "")
	if _, err := s.SaveCredentials(context.Background(), 0, "", ""); err != nil {
		t.Fatal(err)
	}
	st := s.Status()
	if st.APIID != 42 || !st.APIHashSet {
		t.Fatalf("已有凭证被改动: %+v", st)
	}
}

// TestLoginBotRequiresAPIID 缺 api_id 时在联网前失败。
func TestLoginBotRequiresAPIID(t *testing.T) {
	s, _ := newService(t, 0, "hash", "token")
	if _, err := s.LoginBot(context.Background(), "token"); err == nil {
		t.Fatal("缺 api_id 应报错")
	}
}

// TestLoginBotRequiresToken 没 token 且没保存过 token 时报错。
func TestLoginBotRequiresToken(t *testing.T) {
	s, _ := newService(t, 1, "hash", "")
	if _, err := s.LoginBot(context.Background(), "  "); err == nil {
		t.Fatal("缺 token 应报错")
	}
}

// TestStartUserLoginRequiresPhone 手机号为空直接拒绝。
func TestStartUserLoginRequiresPhone(t *testing.T) {
	s, _ := newService(t, 1, "hash", "")
	if _, err := s.StartUserLogin(context.Background(), "  "); err == nil {
		t.Fatal("空手机号应报错")
	}
}

// TestFinishUserLoginRequiresCode 缺验证码或 hash 直接拒绝。
func TestFinishUserLoginRequiresCode(t *testing.T) {
	s, _ := newService(t, 1, "hash", "")
	if _, err := s.FinishUserLogin(context.Background(), FinishRequest{Phone: "+8613800000000"}); err == nil {
		t.Fatal("缺验证码应报错")
	}
}

// TestIdentityWithoutSession 没有会话文件时报「先登录」。
func TestIdentityWithoutSession(t *testing.T) {
	s, _ := newService(t, 1, "hash", "")
	if _, err := s.Identity(context.Background(), true); err == nil {
		t.Fatal("缺会话应报错")
	}
}

// TestNormalizePhone 只保留数字与前导 +，其余符号全部丢掉。
//
// 输入：带空格、横线、括号的号码
// 输出：紧凑号码
// 预期：+86 138-0000 0000 变成 +8613800000000
func TestNormalizePhone(t *testing.T) {
	cases := map[string]string{
		" +86 138-0000 0000 ": "+8613800000000",
		"(010) 1234":          "0101234",
		"":                    "",
		"   ":                 "",
	}
	for in, want := range cases {
		if got := normalizePhone(in); got != want {
			t.Errorf("normalizePhone(%q) = %q, want %q", in, got, want)
		}
	}
}
