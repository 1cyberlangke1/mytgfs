package crypto

// AES-256-GCM 加密工具。
//
// 布局：TDS1(4) | nonce(12) | ciphertext+tag
// magic 前缀用于区分「已加密」与「旧明文」。

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"mytgfs/internal/i18n"
)

const (
	// KeySize 是 AES-256 密钥长度。
	KeySize = 32
	magic   = "TDS1"
)

// ErrLegacyPlain 表示输入不是本包加密的格式。
// 用类型而不是包级字符串：文案要按当前语言惰性求值。
type errLegacyPlain struct{}

func (errLegacyPlain) Error() string { return i18n.T("err.crypto_legacy") }

var ErrLegacyPlain error = errLegacyPlain{}

// LoadOrCreateKey 读取 hex 编码的 32 字节密钥；不存在时生成并写入。
//
// 输入：path 密钥文件路径
// 输出：32 字节密钥
// 预期：首次调用自动创建 0600 文件，之后复用
func LoadOrCreateKey(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err == nil {
		key, derr := hex.DecodeString(strings.TrimSpace(string(raw)))
		if derr != nil {
			return nil, fmt.Errorf(i18n.T("err.key_decode")+": %w", derr)
		}
		if len(key) != KeySize {
			return nil, fmt.Errorf(i18n.T("err.key_length"), len(key), KeySize)
		}
		return key, nil
	}
	if !os.IsNotExist(err) {
		return nil, err
	}
	key := make([]byte, KeySize)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	// 首次运行 secrets/ 可能还不存在，这里一并创建
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, []byte(hex.EncodeToString(key)+"\n"), 0o600); err != nil {
		return nil, err
	}
	return key, nil
}

// Encrypt 用 AES-256-GCM 加密。
//
// 输入：key 32 字节；plaintext 任意字节
// 输出：TDS1 | nonce | ciphertext+tag
// 预期：每次 nonce 随机，同一明文输出不同
func Encrypt(key, plaintext []byte) ([]byte, error) {
	if len(key) != KeySize {
		return nil, errors.New(i18n.T("err.crypto_key_size"))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(magic)+len(nonce)+len(plaintext)+gcm.Overhead())
	out = append(out, magic...)
	out = append(out, nonce...)
	out = gcm.Seal(out, nonce, plaintext, nil)
	return out, nil
}

// Decrypt 反解 Encrypt 的输出。
//
// 输入：key 32 字节；blob 带 TDS1 magic
// 输出：明文
// 预期：无 magic 返回 ErrLegacyPlain
func Decrypt(key, blob []byte) ([]byte, error) {
	if len(blob) < len(magic) || string(blob[:len(magic)]) != magic {
		return nil, ErrLegacyPlain
	}
	if len(key) != KeySize {
		return nil, errors.New(i18n.T("err.crypto_key_size"))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(blob) < len(magic)+gcm.NonceSize() {
		return nil, errors.New(i18n.T("err.crypto_blob_short"))
	}
	nonce := blob[len(magic) : len(magic)+gcm.NonceSize()]
	ciphertext := blob[len(magic)+gcm.NonceSize():]
	return gcm.Open(nil, nonce, ciphertext, nil)
}
