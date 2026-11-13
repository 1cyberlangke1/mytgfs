package web

// 缩略图与详情片段测试：缓存、304、非图片回落。

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"mytgfs/internal/config"
	"mytgfs/internal/fm"
)

// pngBytes 生成一张 4x4 的纯色 PNG。
func pngBytes(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			img.Set(x, y, color.RGBA{R: 10, G: 120, B: 200, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// withFile 往假后端放一个文件。
func withFile(f *fakeFiles, name string, data []byte) {
	f.entries["/"] = append(f.entries["/"], fm.Entry{Name: name, Size: int64(len(data)), Mod: time.Unix(1700000000, 0)})
	f.content["/"+name] = data
}

// TestFileThumbGeneratesAndRevalidates 首次生成 JPEG，带 ETag 的第二次请求返回 304。
func TestFileThumbGeneratesAndRevalidates(t *testing.T) {
	f := newFakeFiles()
	withFile(f, "a.png", pngBytes(t))
	api := newTestAPI(f)
	api.thumbs = newThumbCache(t.TempDir())

	rec := httptest.NewRecorder()
	handleFileThumb(api)(rec, httptest.NewRequest(http.MethodGet, "/files/thumb?vol=demo&path=%2Fa.png", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d body = %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("Content-Type = %q", ct)
	}
	if rec.Body.Len() == 0 {
		t.Fatal("缩略图为空")
	}
	etag := rec.Header().Get("ETag")
	if etag == "" {
		t.Fatal("缺少 ETag")
	}

	req := httptest.NewRequest(http.MethodGet, "/files/thumb?vol=demo&path=%2Fa.png", nil)
	req.Header.Set("If-None-Match", etag)
	rec = httptest.NewRecorder()
	handleFileThumb(api)(rec, req)
	if rec.Code != http.StatusNotModified {
		t.Errorf("二次请求 code = %d，期望 304", rec.Code)
	}
}

// TestFileThumbRejectsNonImage 非图片扩展名直接 415，不读文件。
func TestFileThumbRejectsNonImage(t *testing.T) {
	f := newFakeFiles()
	withFile(f, "a.txt", []byte("hello"))
	rec := httptest.NewRecorder()
	handleFileThumb(newTestAPI(f))(rec,
		httptest.NewRequest(http.MethodGet, "/files/thumb?vol=demo&path=%2Fa.txt", nil))
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("code = %d", rec.Code)
	}
}

// TestFileThumbUnknownVolume 卷不存在返回 404。
func TestFileThumbUnknownVolume(t *testing.T) {
	rec := httptest.NewRecorder()
	handleFileThumb(newTestAPI(newFakeFiles()))(rec,
		httptest.NewRequest(http.MethodGet, "/files/thumb?vol=demo&path=%2Fx.png", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("code = %d", rec.Code)
	}
}

// TestFragDetailsImageHasThumb 图片详情里引用缩略图地址。
func TestFragDetailsImageHasThumb(t *testing.T) {
	f := newFakeFiles()
	data := pngBytes(t)
	withFile(f, "a.png", data)
	rec := httptest.NewRecorder()
	handleFragDetails(newTestAPI(f), newPWAData(config.PWA{}))(rec,
		httptest.NewRequest(http.MethodGet, "/frag/details?vol=demo&path=%2Fa.png", nil))
	body := rec.Body.String()
	if !strings.Contains(body, "/files/thumb?path=%2Fa.png") {
		t.Errorf("缺少缩略图地址: %s", body)
	}
	if !strings.Contains(body, formatBytes(int64(len(data)))) {
		t.Errorf("缺少大小: %s", body)
	}
}

// TestFragDetailsTextNoThumb 文本文件不引用缩略图。
func TestFragDetailsTextNoThumb(t *testing.T) {
	f := newFakeFiles()
	withFile(f, "a.txt", []byte("hello"))
	rec := httptest.NewRecorder()
	handleFragDetails(newTestAPI(f), newPWAData(config.PWA{}))(rec,
		httptest.NewRequest(http.MethodGet, "/frag/details?vol=demo&path=%2Fa.txt", nil))
	if strings.Contains(rec.Body.String(), "/files/thumb") {
		t.Errorf("文本文件不应请求缩略图: %s", rec.Body.String())
	}
}

// TestScaleThumbKeepsAspect 缩放保持长宽比且不超过上限。
func TestScaleThumbKeepsAspect(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 1000, 500))
	dst := scaleThumb(src)
	b := dst.Bounds()
	if b.Dx() != 320 || b.Dy() != 160 {
		t.Errorf("尺寸 = %dx%d", b.Dx(), b.Dy())
	}
}
