package web

// 卷封面接口与卷名校验的单元测试。

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"
)

// fakeCovers 是内存版频道头像源。
type fakeCovers struct {
	volumes map[string]bool
	data    []byte
	version string
	getErr  error
	setErr  error
	setData []byte
}

func (f *fakeCovers) HasVolume(name string) bool { return f.volumes[name] }

func (f *fakeCovers) CoverVersion(context.Context, string) (string, error) {
	return f.version, nil
}

func (f *fakeCovers) CoverImage(context.Context, string) ([]byte, string, error) {
	return f.data, f.version, f.getErr
}

func (f *fakeCovers) SetCover(_ context.Context, _ string, data []byte) error {
	f.setData = data
	return f.setErr
}

// TestCoverHandlerServe 输出头像字节与 ETag。
func TestCoverHandlerServe(t *testing.T) {
	c := &fakeCovers{volumes: map[string]bool{"demo": true}, data: []byte("jpeg"), version: "55"}
	rec := httptest.NewRecorder()
	handleVolumeCover(c).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/cover?vol=demo", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d", rec.Code)
	}
	if rec.Body.String() != "jpeg" {
		t.Errorf("body = %q", rec.Body.String())
	}
	if got := rec.Header().Get("ETag"); got != `"55"` {
		t.Errorf("ETag = %q", got)
	}
	if got := rec.Header().Get("Content-Type"); got != "image/jpeg" {
		t.Errorf("Content-Type = %q", got)
	}
}

// TestCoverHandlerNotModified 版本未变时返回 304。
func TestCoverHandlerNotModified(t *testing.T) {
	c := &fakeCovers{volumes: map[string]bool{"demo": true}, data: []byte("jpeg"), version: "55"}
	req := httptest.NewRequest(http.MethodGet, "/api/cover?vol=demo", nil)
	req.Header.Set("If-None-Match", `"55"`)
	rec := httptest.NewRecorder()
	handleVolumeCover(c).ServeHTTP(rec, req)
	if rec.Code != http.StatusNotModified {
		t.Fatalf("code = %d，期望 304", rec.Code)
	}
}

// TestCoverHandlerNoAvatar 没有头像返回 404。
func TestCoverHandlerNoAvatar(t *testing.T) {
	c := &fakeCovers{volumes: map[string]bool{"demo": true}}
	rec := httptest.NewRecorder()
	handleVolumeCover(c).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/cover?vol=demo", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("code = %d，期望 404", rec.Code)
	}
}

// TestCoverHandlerUnknownVolume 卷不存在返回 404。
func TestCoverHandlerUnknownVolume(t *testing.T) {
	c := &fakeCovers{volumes: map[string]bool{}}
	rec := httptest.NewRecorder()
	handleVolumeCover(c).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/cover?vol=nope", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("code = %d，期望 404", rec.Code)
	}
}

// TestCoverHandlerUpload 上传原图后写入的是 512×512 JPEG。
func TestCoverHandlerUpload(t *testing.T) {
	c := &fakeCovers{volumes: map[string]bool{"demo": true}}
	rec := httptest.NewRecorder()
	handleVolumeCover(c).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/cover?vol=demo", bytes.NewReader(makePNG(t, 300, 200))))
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d body=%s", rec.Code, rec.Body.String())
	}
	img, err := jpeg.Decode(bytes.NewReader(c.setData))
	if err != nil {
		t.Fatalf("写入内容不是 JPEG: %v", err)
	}
	if b := img.Bounds(); b.Dx() != 512 || b.Dy() != 512 {
		t.Fatalf("写入尺寸 = %d×%d", b.Dx(), b.Dy())
	}
}

// TestCoverHandlerUploadBadImage 非图片与空体都不写频道。
func TestCoverHandlerUploadBadImage(t *testing.T) {
	c := &fakeCovers{volumes: map[string]bool{"demo": true}}
	for _, body := range [][]byte{[]byte("nope"), {}} {
		rec := httptest.NewRecorder()
		handleVolumeCover(c).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/cover?vol=demo", bytes.NewReader(body)))
		if rec.Code != http.StatusUnsupportedMediaType && rec.Code != http.StatusBadRequest {
			t.Fatalf("body=%q code = %d，期望 415/400", body, rec.Code)
		}
	}
	if c.setData != nil {
		t.Error("坏输入不该写频道头像")
	}
}

// TestCoverHandlerSetError Telegram 失败返回 502。
func TestCoverHandlerSetError(t *testing.T) {
	c := &fakeCovers{volumes: map[string]bool{"demo": true}, setErr: errors.New("boom")}
	rec := httptest.NewRecorder()
	handleVolumeCover(c).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/cover?vol=demo", bytes.NewReader(makePNG(t, 10, 10))))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("code = %d，期望 502", rec.Code)
	}
}

// TestCoverHandlerUnavailable 未接线时返回 501。
func TestCoverHandlerUnavailable(t *testing.T) {
	rec := httptest.NewRecorder()
	handleVolumeCover(nil).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/cover?vol=demo", nil))
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("code = %d，期望 501", rec.Code)
	}
}

// TestCoverHandlerMethod 其它方法返回 405。
func TestCoverHandlerMethod(t *testing.T) {
	c := &fakeCovers{volumes: map[string]bool{"demo": true}}
	rec := httptest.NewRecorder()
	handleVolumeCover(c).ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/cover?vol=demo", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("code = %d，期望 405", rec.Code)
	}
}

// makePNG 生成纯色 PNG。
func makePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{R: 200, G: 100, B: 50, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("生成 PNG: %v", err)
	}
	return buf.Bytes()
}

// TestValidVolumeName 卷名只允许安全字符。
//
// 输入：合法与非法卷名
// 输出：布尔
// 预期：斜杠、空格、中文、超长都拒绝，避免拼进 index-<name>.db 出问题
func TestValidVolumeName(t *testing.T) {
	for _, n := range []string{"demo", "demo2", "a-b_c", "A1"} {
		if !validVolumeName(n) {
			t.Errorf("%q 应合法", n)
		}
	}
	for _, n := range []string{"", "../x", "a/b", "中文", "a b", string(make([]byte, 40))} {
		if validVolumeName(n) {
			t.Errorf("%q 应非法", n)
		}
	}
}
