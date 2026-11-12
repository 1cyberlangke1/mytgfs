package sessionstore

// 加密的 Telegram session 存储，实现 gotd 的 session.Storage。

import (
	"context"
	"os"
	"path/filepath"

	tgsession "github.com/gotd/td/session"

	"mytgfs/internal/crypto"
)

// Encrypted 是加密的 session 存储。
type Encrypted struct {
	Path string // session 文件路径
	Key  []byte // 32 字节 AES-256 密钥
}

// LoadSession 读取并解密 session。
//
// 输入：ctx
// 输出：session 字节；文件不存在返回 (nil, nil)
// 预期：读到旧明文时自动加密重写后再返回
func (s *Encrypted) LoadSession(ctx context.Context) ([]byte, error) {
	raw, err := os.ReadFile(s.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	plain, derr := crypto.Decrypt(s.Key, raw)
	if derr == crypto.ErrLegacyPlain {
		if err := s.StoreSession(ctx, raw); err != nil {
			return nil, err
		}
		return raw, nil
	}
	return plain, derr
}

// StoreSession 加密并原子写入 session。
//
// 输入：ctx；data 原始 session 字节
// 输出：error
// 预期：先写临时文件再 rename，断电不会留下半截文件
func (s *Encrypted) StoreSession(ctx context.Context, data []byte) error {
	enc, err := crypto.Encrypt(s.Key, data)
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.Path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".session-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(enc); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.Path)
}

// 编译期断言：Encrypted 满足 gotd 的 session.Storage。
var _ tgsession.Storage = (*Encrypted)(nil)
