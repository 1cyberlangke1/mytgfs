package cover

// 头像归一的单测：只验证几何与编码，不连 Telegram。

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

// makePNG 生成一张纯色 PNG，尺寸用于验证裁切与缩放。
func makePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("生成 PNG: %v", err)
	}
	return buf.Bytes()
}

// TestSquareJPEGCropsToSquare 任意长宽比都输出 Side×Side 的 JPEG。
//
// 输入：方图、横图、竖图、极小图
// 输出：JPEG 字节
// 预期：解码后边界恒为 512×512，且是合法 JPEG
func TestSquareJPEGCropsToSquare(t *testing.T) {
	cases := []struct{ w, h int }{
		{100, 100},
		{400, 200},
		{200, 400},
		{16, 16},
		{1024, 1024},
	}
	for _, c := range cases {
		out, err := SquareJPEG(makePNG(t, c.w, c.h))
		if err != nil {
			t.Fatalf("%dx%d: %v", c.w, c.h, err)
		}
		img, err := jpeg.Decode(bytes.NewReader(out))
		if err != nil {
			t.Fatalf("%dx%d 输出不是 JPEG: %v", c.w, c.h, err)
		}
		b := img.Bounds()
		if b.Dx() != Side || b.Dy() != Side {
			t.Fatalf("%dx%d 输出 %dx%d，期望 %d×%d", c.w, c.h, b.Dx(), b.Dy(), Side, Side)
		}
	}
}

// TestSquareJPEGCenterCrop 横图裁掉两侧而不是压扁。
//
// 输入：左右两半不同颜色的 200×100 图
// 输出：JPEG
// 预期：裁切后左右边缘颜色接近原图中心区域，说明是从中心取正方形
func TestSquareJPEGCenterCrop(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 200, 100))
	for y := range 100 {
		for x := range 200 {
			c := color.RGBA{R: 255, A: 255}
			if x >= 100 {
				c = color.RGBA{B: 255, A: 255}
			}
			img.Set(x, y, c)
		}
	}
	var raw bytes.Buffer
	if err := png.Encode(&raw, img); err != nil {
		t.Fatal(err)
	}
	out, err := SquareJPEG(raw.Bytes())
	if err != nil {
		t.Fatalf("SquareJPEG: %v", err)
	}
	got, err := jpeg.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	// 中心正方形（x 50..150）左端应是红、右端应是蓝
	r, _, _, _ := got.At(0, Side/2).RGBA()
	_, _, b, _ := got.At(Side-1, Side/2).RGBA()
	if r>>8 < 200 || b>>8 < 200 {
		t.Fatalf("中心裁切结果不符: 左 r=%d 右 b=%d", r>>8, b>>8)
	}
}

// TestSquareJPEGRejectsGarbage 非图片输入返回 ErrNoImage。
func TestSquareJPEGRejectsGarbage(t *testing.T) {
	if _, err := SquareJPEG([]byte("not an image")); !errors.Is(err, ErrNoImage) {
		t.Fatalf("err = %v，期望 ErrNoImage", err)
	}
	if _, err := SquareJPEG(nil); !errors.Is(err, ErrNoImage) {
		t.Fatalf("空输入 err = %v，期望 ErrNoImage", err)
	}
}
