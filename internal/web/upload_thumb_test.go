package web

// 上传缩略图的入参校验：只接受合规的小 JPEG。

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

// TestSaveThumbAcceptsJPEG 合规小图落盘且字节一致。
func TestSaveThumbAcceptsJPEG(t *testing.T) {
	jpeg := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 0x4A, 0x46, 0x49, 0x46}
	got := saveThumb(t.TempDir(), jpeg)
	if got == "" {
		t.Fatal("合规 JPEG 应该被接受")
	}
	t.Cleanup(func() { _ = os.Remove(got) })
	b, err := os.ReadFile(got)
	if err != nil {
		t.Fatalf("读回落盘文件失败: %v", err)
	}
	if len(b) != len(jpeg) || b[0] != 0xFF || b[1] != 0xD8 {
		t.Fatalf("落盘内容被改动: %v", b)
	}
}

// TestSaveThumbRejectsInvalid 非 JPEG、超大、空串一律丢弃。
func TestSaveThumbRejectsInvalid(t *testing.T) {
	dir := t.TempDir()
	jpeg := []byte{0xFF, 0xD8, 0xFF, 0xE0}
	png := []byte{0x89, 'P', 'N', 'G'}
	oversize := make([]byte, thumbMaxBytes+1)
	copy(oversize, jpeg)

	cases := map[string][]byte{
		"空":    nil,
		"PNG":  png,
		"超过上限": oversize,
	}
	for name, in := range cases {
		if got := saveThumb(dir, in); got != "" {
			t.Errorf("%s: 应被拒绝，实际落盘 %s", name, got)
			_ = os.Remove(got)
		}
	}
}

// TestUploadThumbReachesAdopt 缩略图经会话一路传到文件后端。
//
// 输入：init -> 分片 -> PUT thumb -> commit
// 输出：AdoptUpload 收到可读的 JPEG 临时文件
// 预期：抽帧与分片并行，缩略图不会丢，也不会被当成内容
func TestUploadThumbReachesAdopt(t *testing.T) {
	f := newFakeFiles()
	api := newTestUploadAPI(t, f)
	id := initSession(t, api, `{"vol":"demo","dir":"/","name":"a.mp4","size":5}`)
	if rec := putChunk(t, api, id, 0, "hello"); rec.Code != http.StatusOK {
		t.Fatalf("分片 code = %d", rec.Code)
	}
	jpeg := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10}
	rec := httptest.NewRecorder()
	api.handleThumb(rec, httptest.NewRequest(http.MethodPut, "/api/upload/thumb?id="+id, bytes.NewReader(jpeg)))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("thumb code = %d body = %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	api.handleCommit(rec, httptest.NewRequest(http.MethodPost, "/api/upload/commit?id="+id, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("commit code = %d body = %s", rec.Code, rec.Body.String())
	}
	if f.lastThumb == "" {
		t.Fatal("缩略图没有传给文件后端")
	}
	t.Cleanup(func() { _ = os.Remove(f.lastThumb) })
	b, err := os.ReadFile(f.lastThumb)
	if err != nil || len(b) != len(jpeg) {
		t.Fatalf("缩略图内容不符: %v len=%d", err, len(b))
	}
}

// TestUploadThumbRejectsNonJPEG 非 JPEG 返回 400，且不会污染会话。
func TestUploadThumbRejectsNonJPEG(t *testing.T) {
	f := newFakeFiles()
	api := newTestUploadAPI(t, f)
	id := initSession(t, api, `{"vol":"demo","dir":"/","name":"a.mp4","size":5}`)
	rec := httptest.NewRecorder()
	api.handleThumb(rec, httptest.NewRequest(http.MethodPut, "/api/upload/thumb?id="+id, bytes.NewReader([]byte{0x89, 'P', 'N', 'G'})))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400", rec.Code)
	}
}

// TestAdoptAcceptsThumbPath 原生选择器路径：thumb 查询串一路传到 AdoptUpload。
//
// 输入：POST /api/upload/adopt?vol&dir&name&path&thumb
// 输出：AdoptUpload 收到该路径；内容仍是视频本体的字节
// 预期：安卓侧抽帧不需要经过浏览器，也不改上传主流程
func TestAdoptAcceptsThumbPath(t *testing.T) {
	f := newFakeFiles()
	api := newTestUploadAPI(t, f)
	dir := t.TempDir()
	video := filepath.Join(dir, "v.mp4")
	if err := os.WriteFile(video, []byte("0123456789"), 0o600); err != nil {
		t.Fatal(err)
	}
	thumb := filepath.Join(dir, "t.jpg")
	if err := os.WriteFile(thumb, []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x01}, 0o600); err != nil {
		t.Fatal(err)
	}
	q := "?vol=demo&dir=/&name=v.mp4&path=" + url.QueryEscape(video) + "&thumb=" + url.QueryEscape(thumb)
	rec := httptest.NewRecorder()
	api.handleAdopt(rec, httptest.NewRequest(http.MethodPost, "/api/upload/adopt"+q, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("adopt code = %d body = %s", rec.Code, rec.Body.String())
	}
	if f.lastThumb != thumb {
		t.Fatalf("lastThumb = %q, want %q", f.lastThumb, thumb)
	}
	if got := f.uploads["/v.mp4"]; got != "0123456789" {
		t.Fatalf("内容不符: %q", got)
	}
}
