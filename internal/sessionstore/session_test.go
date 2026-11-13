package sessionstore

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestStoreLoadRoundTrip 存进去的 session 能原样读回，且落盘内容不是明文。
//
// 输入：同一密钥写入再读取
// 输出：读回字节与写入一致；磁盘文件不含明文
func TestStoreLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := &Encrypted{Path: filepath.Join(dir, "sub", "bot.session"), Key: bytes.Repeat([]byte{9}, 32)}
	plain := []byte("telegram session payload")

	if err := s.StoreSession(context.Background(), plain); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(s.Path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, plain) {
		t.Fatal("磁盘上出现了明文 session")
	}

	got, err := s.LoadSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("读回内容不一致: %q", got)
	}
}

// TestLoadMissingReturnsNil 文件不存在时返回 (nil, nil)，首次登录依赖这个语义。
func TestLoadMissingReturnsNil(t *testing.T) {
	s := &Encrypted{Path: filepath.Join(t.TempDir(), "absent.session"), Key: bytes.Repeat([]byte{9}, 32)}
	got, err := s.LoadSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("期望 nil，得到 %q", got)
	}
}

// TestLoadUpgradesLegacyPlain 旧明文 session 读时自动加密重写。
func TestLoadUpgradesLegacyPlain(t *testing.T) {
	p := filepath.Join(t.TempDir(), "old.session")
	plain := []byte("legacy plain session")
	if err := os.WriteFile(p, plain, 0o600); err != nil {
		t.Fatal(err)
	}
	s := &Encrypted{Path: p, Key: bytes.Repeat([]byte{5}, 32)}

	got, err := s.LoadSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("读回内容不一致: %q", got)
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, plain) {
		t.Fatal("旧明文没有被加密重写")
	}
}

// TestLoadRejectsWrongKey 密钥不对时不能把乱码当 session 返回。
func TestLoadRejectsWrongKey(t *testing.T) {
	dir := t.TempDir()
	write := &Encrypted{Path: filepath.Join(dir, "s.session"), Key: bytes.Repeat([]byte{1}, 32)}
	if err := write.StoreSession(context.Background(), []byte("data")); err != nil {
		t.Fatal(err)
	}
	read := &Encrypted{Path: write.Path, Key: bytes.Repeat([]byte{2}, 32)}
	if _, err := read.LoadSession(context.Background()); err == nil {
		t.Fatal("错误密钥竟然读出了 session")
	}
}
