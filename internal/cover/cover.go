package cover

// 频道头像的图片归一：把任意图片裁成正方形并编码成 Telegram 能接受的 JPEG。
//
// Telegram 头像必须是正方形、且单次 upload.saveFilePart 的负载不宜过大，
// 所以这里统一裁方、缩到 512×512、按质量 90 编码。

import (
	"bytes"
	"image"
	"image/jpeg"

	// 注册常见图片格式的解码器：头像允许直接传 PNG/JPEG/GIF/WebP
	_ "image/gif"
	_ "image/png"

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"

	"mytgfs/internal/i18n"
)

// Side 是输出头像的边长（像素）。
const Side = 512

// ErrNoImage 表示输入不是可解码的图片。
//
// 输入：无（哨兵）
// 输出：Error() 在调用时才取当前语言文案
// 预期：包级只存类型，语言切换后文案仍然正确
var ErrNoImage error = errNoImage{}

type errNoImage struct{}

func (errNoImage) Error() string { return i18n.T("err.cover_decode") }

// SquareJPEG 把任意图片裁成 Side×Side 的 JPEG。
//
// 输入：raw 原始图片字节（PNG/JPEG/GIF/WebP）
// 输出：jpeg 编码后的正方形头像
// 预期：
//   - 非正方形按中心裁切，保留主体而不是压扁
//   - 小图放大到 Side，大图缩小，最终尺寸恒为 Side×Side
//   - 无法解码时返回 ErrNoImage，绝不返回半成品
func SquareJPEG(raw []byte) ([]byte, error) {
	src, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, ErrNoImage
	}
	dst := image.NewRGBA(image.Rect(0, 0, Side, Side))
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return nil, ErrNoImage
	}
	// 中心裁出最大正方形，再整体缩放到目标尺寸
	side := min(w, h)
	sx := b.Min.X + (w-side)/2
	sy := b.Min.Y + (h-side)/2
	crop := image.Rect(sx, sy, sx+side, sy+side)
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, crop, xdraw.Src, nil)

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 90}); err != nil {
		return nil, ErrNoImage
	}
	return buf.Bytes(), nil
}
