package web

// bodyFromSrc 单测：原生选择器只给一个绝对路径，服务端要把它换成文件句柄。
// 安卓上这个路径形如 /proc/self/fd/N，Linux 分支会用 dup 打开，这里验证
// 普通绝对路径下的替换与失败回退。

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// TestBodyFromSrcReadsAbsolutePath 绝对路径会被换成文件句柄。
//
// 输入：src 指向存在的临时文件
// 输出：r.Body 读到文件内容
// 预期：路径合法时不再走原始请求体
func TestBodyFromSrcReadsAbsolutePath(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "cover.bin")
	if err := os.WriteFile(src, []byte("cover-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/cover?src="+src, nil)
	r.Body = io.NopCloser(&emptyReader{})

	bodyFromSrc(r)
	got, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	// Windows 上不关句柄会挡住 TempDir 清理
	if c, ok := r.Body.(io.Closer); ok {
		_ = c.Close()
	}
	if string(got) != "cover-bytes" {
		t.Fatalf("body = %q, want cover-bytes", got)
	}
}

// TestBodyFromSrcKeepsBodyOnMissingPath 路径不可读时不动原始请求体。
//
// 输入：src 指向不存在的文件
// 输出：r.Body 仍是原来的空体
// 预期：后续处理按请求体为空返回 400，而不是偷偷读到别的东西
func TestBodyFromSrcKeepsBodyOnMissingPath(t *testing.T) {
	src := filepath.Join(t.TempDir(), "missing.bin")
	r := httptest.NewRequest(http.MethodPost, "/api/cover?src="+src, nil)
	original := r.Body

	bodyFromSrc(r)
	if r.Body != original {
		t.Fatal("missing src 不该替换 r.Body")
	}
}

// emptyReader 是一个永远返回 EOF 的读取器，用来标记原始请求体。
type emptyReader struct{}

func (*emptyReader) Read([]byte) (int, error) { return 0, io.EOF }
