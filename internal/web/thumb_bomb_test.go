package web

// 缩略图解码上限：高压缩比图片不能靠「解出来再缩」把进程内存打满。

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"
)

// pngChunk 追加一个带 CRC 的 PNG chunk。
func pngChunk(buf *bytes.Buffer, typ string, data []byte) {
	_ = binary.Write(buf, binary.BigEndian, uint32(len(data)))
	chunk := append([]byte(typ), data...)
	buf.Write(chunk)
	_ = binary.Write(buf, binary.BigEndian, crc32.ChecksumIEEE(chunk))
}

// hugePNGHeader 造一个只有文件头、声明尺寸巨大的 PNG。
//
// 输入：宽、高像素
// 输出：PNG 字节（含签名、IHDR、IEND，无像素数据）
// 预期：DecodeConfig 能读到声明尺寸；整图解码会失败，正好验证「先看头就拒绝」
func hugePNGHeader(w, h uint32) []byte {
	var buf bytes.Buffer
	buf.Write([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'})
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:4], w)
	binary.BigEndian.PutUint32(ihdr[4:8], h)
	ihdr[8] = 8 // bit depth
	ihdr[9] = 2 // color type: truecolor
	pngChunk(&buf, "IHDR", ihdr)
	pngChunk(&buf, "IEND", nil)
	return buf.Bytes()
}

// TestGenerateThumbRejectsPixelBomb 超大声明尺寸直接拒绝，不进入整图解码。
func TestGenerateThumbRejectsPixelBomb(t *testing.T) {
	files := newWindowFiles(hugePNGHeader(20000, 20000)) // 4 亿像素 > thumbMaxPixels
	req := httptest.NewRequest(http.MethodGet, "/clip.mp4", nil)
	if got := generateThumb(req, files, "/clip.mp4", 128); got != nil {
		t.Fatalf("像素炸弹应返回 nil，实际得到 %d 字节", len(got))
	}
}

// TestGenerateThumbScalesSmallImage 合法小图正常产出 JPEG。
func TestGenerateThumbScalesSmallImage(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 16, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 16; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 16), G: uint8(y * 32), B: 128, A: 255})
		}
	}
	var src bytes.Buffer
	if err := png.Encode(&src, img); err != nil {
		t.Fatalf("编码测试 PNG: %v", err)
	}
	files := newWindowFiles(src.Bytes())
	req := httptest.NewRequest(http.MethodGet, "/clip.mp4", nil)
	got := generateThumb(req, files, "/clip.mp4", 8)
	if len(got) == 0 {
		t.Fatal("合法小图应产出 JPEG")
	}
	if _, err := jpeg.Decode(bytes.NewReader(got)); err != nil {
		t.Fatalf("输出不是可解码的 JPEG: %v", err)
	}
}
