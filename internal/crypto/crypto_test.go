package crypto

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestLoadOrCreateKeyCreatesAndReuses 密钥文件只生成一次，后续读取必须一致。
//
// 输入：同一个临时路径调用两次
// 输出：两次 32 字节密钥完全相同
func TestLoadOrCreateKeyCreatesAndReuses(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "session.key")
	first, err := LoadOrCreateKey(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 32 {
		t.Fatalf("密钥长度 = %d，期望 32", len(first))
	}
	second, err := LoadOrCreateKey(p)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("第二次读取的密钥与第一次不同")
	}
}

// TestEncryptDecryptRoundTrip 明文经加密再解密必须还原。
func TestEncryptDecryptRoundTrip(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	for _, size := range []int{0, 1, 15, 16, 1024} {
		plain := bytes.Repeat([]byte("a"), size)
		blob, err := Encrypt(key, plain)
		if err != nil {
			t.Fatalf("size=%d 加密失败: %v", size, err)
		}
		if size > 0 && bytes.Contains(blob, plain) {
			t.Fatalf("size=%d 密文里出现了明文", size)
		}
		got, err := Decrypt(key, blob)
		if err != nil {
			t.Fatalf("size=%d 解密失败: %v", size, err)
		}
		if !bytes.Equal(got, plain) {
			t.Fatalf("size=%d 解密结果不一致", size)
		}
	}
}

// TestDecryptRejectsWrongKey 换错密钥必须报错而不是返回垃圾。
func TestDecryptRejectsWrongKey(t *testing.T) {
	blob, err := Encrypt(bytes.Repeat([]byte{1}, 32), []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decrypt(bytes.Repeat([]byte{2}, 32), blob); err == nil {
		t.Fatal("错误密钥竟然解密成功")
	}
}

// TestDecryptRejectsTampered 篡改密文必须被校验拦下。
func TestDecryptRejectsTampered(t *testing.T) {
	key := bytes.Repeat([]byte{3}, 32)
	blob, err := Encrypt(key, []byte("secret payload"))
	if err != nil {
		t.Fatal(err)
	}
	blob[len(blob)-1] ^= 0xff
	if _, err := Decrypt(key, blob); err == nil {
		t.Fatal("被篡改的密文竟然解密成功")
	}
}

// TestDecryptDetectsLegacyPlain 旧版明文文件要返回可识别的哨兵错误。
func TestDecryptDetectsLegacyPlain(t *testing.T) {
	if _, err := Decrypt(bytes.Repeat([]byte{4}, 32), []byte("plain session data")); !isLegacy(err) {
		t.Fatalf("错误 = %v，期望旧明文哨兵", err)
	}
}

// isLegacy 判断是否命中旧明文哨兵，避免测试直接依赖错误展开细节。
func isLegacy(err error) bool { return err != nil && err == ErrLegacyPlain }

// TestLoadOrCreateKeyRejectsBadFile 密钥文件长度不对时不能静默接受。
func TestLoadOrCreateKeyRejectsBadFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "bad.key")
	if err := os.WriteFile(p, []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateKey(p); err == nil {
		t.Fatal("长度错误的密钥文件竟然被接受")
	}
}
