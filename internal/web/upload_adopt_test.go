package web

// 按路径接管（/api/upload/adopt）的单测：面板只给路径，服务端读盘并入库。

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestUploadAPIAdoptPath 校验路径接管的主路径。
//
// 输入：磁盘上一个已存在的文件，请求体只带 vol/dir/name/path
// 输出：200；文件后端收到该内容，摘要等于内容摘要
// 预期：请求体里没有任何字节字段，等价于「选择器把文件落盘后只交路径」
func TestUploadAPIAdoptPath(t *testing.T) {
	f := newFakeFiles()
	api := newTestUploadAPI(t, f)
	src := filepath.Join(t.TempDir(), "picked.bin")
	const content = "hello adopt"
	if err := os.WriteFile(src, []byte(content), 0o600); err != nil {
		t.Fatalf("写测试文件失败: %v", err)
	}
	body := fmt.Sprintf(`{"vol":"v","dir":"/","name":"picked.bin","path":%q}`, src)
	rec := httptest.NewRecorder()
	api.handleAdopt(rec, httptest.NewRequest(http.MethodPost, "/api/upload/adopt", strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if f.adoptCalls != 1 {
		t.Fatalf("AdoptUpload 调用 %d 次，期望 1", f.adoptCalls)
	}
	if got := f.uploads["/picked.bin"]; got != content {
		t.Fatalf("落盘内容 = %q，期望 %q", got, content)
	}
	sum := sha256.Sum256([]byte(content))
	if want := hex.EncodeToString(sum[:]); f.lastSha != want {
		t.Fatalf("摘要 = %q，期望 %q", f.lastSha, want)
	}
}

// TestUploadAPIAdoptRejects 覆盖非法输入。
//
// 输入：相对路径、目录、空文件、缺字段
// 输出：均为 400，且不触发接管
// 预期：这些情况绝不能把服务端的任意路径当文件读
func TestUploadAPIAdoptRejects(t *testing.T) {
	f := newFakeFiles()
	api := newTestUploadAPI(t, f)
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty.bin")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatalf("写空文件失败: %v", err)
	}
	cases := []struct {
		name string
		body string
	}{
		{"相对路径", `{"vol":"v","dir":"/","name":"a.bin","path":"a.bin"}`},
		{"目录", fmt.Sprintf(`{"vol":"v","dir":"/","name":"a","path":%q}`, dir)},
		{"空文件", fmt.Sprintf(`{"vol":"v","dir":"/","name":"empty.bin","path":%q}`, empty)},
		{"缺路径", `{"vol":"v","dir":"/","name":"a.bin"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			api.handleAdopt(rec, httptest.NewRequest(http.MethodPost, "/api/upload/adopt", strings.NewReader(c.body)))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s，期望 400", rec.Code, rec.Body.String())
			}
		})
	}
	if f.adoptCalls != 0 {
		t.Fatalf("非法请求触发了 %d 次接管", f.adoptCalls)
	}
}
