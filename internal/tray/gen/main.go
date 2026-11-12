//go:build ignore

// 图标生成器：Telegram 纸飞机 + 右下角硬盘徽章。
//
// 输入：无（尺寸与配色写死在常量里）
// 输出：assets/icon.ico（Windows 多尺寸）与 assets/icon.png（其它平台）
// 预期：go run gen/main.go 覆盖生成；所有尺寸都用 4 倍超采样再降采样抗锯齿，
//
//	不依赖任何第三方绘图库
package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
)

// ss 是超采样倍数：按 ss 倍尺寸绘制，再按 ss×ss 方块平均降采样。
const ss = 4

// rgba 是归一化的非预乘颜色。
type rgba struct {
	r, g, b, a float64
}

var (
	blue   = rgba{0.13, 0.62, 0.85, 1} // Telegram 蓝
	blueDk = rgba{0.09, 0.47, 0.68, 1} // 外圈深蓝
	white  = rgba{1, 1, 1, 1}
	fold   = rgba{0.80, 0.91, 0.97, 1} // 纸飞机折面
	metal  = rgba{0.29, 0.35, 0.41, 1} // 硬盘主体
	metalL = rgba{0.44, 0.51, 0.58, 1} // 硬盘上盖
	slot   = rgba{0.13, 0.18, 0.23, 1} // 硬盘缝隙
	led    = rgba{0.24, 0.86, 0.52, 1} // 硬盘指示灯
	shadow = rgba{0, 0, 0, 0.20}
)

// canvas 是超采样画布，坐标使用 0..1 归一化。
type canvas struct {
	w, h int
	pix  []rgba
}

func newCanvas(n int) *canvas {
	c := &canvas{w: n * ss, h: n * ss}
	c.pix = make([]rgba, c.w*c.h)
	return c
}

// set 按 source-over 合成一个像素。
func (c *canvas) set(x, y int, col rgba) {
	if x < 0 || y < 0 || x >= c.w || y >= c.h {
		return
	}
	i := y*c.w + x
	if col.a >= 1 {
		c.pix[i] = col
		return
	}
	d := c.pix[i]
	outA := col.a + d.a*(1-col.a)
	if outA <= 0 {
		c.pix[i] = rgba{}
		return
	}
	c.pix[i] = rgba{
		r: (col.r*col.a + d.r*d.a*(1-col.a)) / outA,
		g: (col.g*col.a + d.g*d.a*(1-col.a)) / outA,
		b: (col.b*col.a + d.b*d.a*(1-col.a)) / outA,
		a: outA,
	}
}

// disc 填充归一化坐标里的实心圆。
func (c *canvas) disc(cx, cy, r float64, col rgba) {
	w := float64(c.w)
	x0 := int(math.Floor((cx - r) * w))
	x1 := int(math.Ceil((cx + r) * w))
	y0 := int(math.Floor((cy - r) * w))
	y1 := int(math.Ceil((cy + r) * w))
	r2 := r * r
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			dx := (float64(x)+0.5)/w - cx
			dy := (float64(y)+0.5)/w - cy
			if dx*dx+dy*dy <= r2 {
				c.set(x, y, col)
			}
		}
	}
}

// point 是归一化坐标里的一个点。
type point struct{ x, y float64 }

// polygon 用奇偶规则填充多边形。
func (c *canvas) polygon(pts []point, col rgba) {
	if len(pts) < 3 {
		return
	}
	minX, minY := pts[0].x, pts[0].y
	maxX, maxY := pts[0].x, pts[0].y
	for _, p := range pts[1:] {
		minX = math.Min(minX, p.x)
		minY = math.Min(minY, p.y)
		maxX = math.Max(maxX, p.x)
		maxY = math.Max(maxY, p.y)
	}
	w := float64(c.w)
	x0 := int(math.Floor(minX * w))
	x1 := int(math.Ceil(maxX * w))
	y0 := int(math.Floor(minY * w))
	y1 := int(math.Ceil(maxY * w))
	for y := y0; y <= y1; y++ {
		py := (float64(y) + 0.5) / w
		for x := x0; x <= x1; x++ {
			px := (float64(x) + 0.5) / w
			if inside(px, py, pts) {
				c.set(x, y, col)
			}
		}
	}
}

// inside 判断点是否在多边形内（射线法）。
func inside(px, py float64, pts []point) bool {
	in := false
	j := len(pts) - 1
	for i := 0; i < len(pts); i++ {
		pi, pj := pts[i], pts[j]
		if (pi.y > py) != (pj.y > py) {
			xi := (pj.x-pi.x)*(py-pi.y)/(pj.y-pi.y) + pi.x
			if px < xi {
				in = !in
			}
		}
		j = i
	}
	return in
}

// roundRect 填充归一化坐标里的圆角矩形。
func (c *canvas) roundRect(x0, y0, x1, y1, r float64, col rgba) {
	w := float64(c.w)
	ix0 := int(math.Floor(x0 * w))
	ix1 := int(math.Ceil(x1 * w))
	iy0 := int(math.Floor(y0 * w))
	iy1 := int(math.Ceil(y1 * w))
	for y := iy0; y <= iy1; y++ {
		py := (float64(y) + 0.5) / w
		if py < y0 || py > y1 {
			continue
		}
		for x := ix0; x <= ix1; x++ {
			px := (float64(x) + 0.5) / w
			if px < x0 || px > x1 {
				continue
			}
			// 把点夹到圆角圆心的允许区内再算距离
			cx := math.Min(math.Max(px, x0+r), x1-r)
			cy := math.Min(math.Max(py, y0+r), y1-r)
			dx, dy := px-cx, py-cy
			if dx*dx+dy*dy <= r*r {
				c.set(x, y, col)
			}
		}
	}
}

// image 降采样成 n×n 的 NRGBA。
func (c *canvas) image(n int) *image.NRGBA {
	out := image.NewNRGBA(image.Rect(0, 0, n, n))
	cnt := float64(ss * ss)
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			var r, g, b, a float64
			for dy := 0; dy < ss; dy++ {
				row := (y*ss + dy) * c.w
				for dx := 0; dx < ss; dx++ {
					p := c.pix[row+x*ss+dx]
					// 预乘后平均，边缘才不会发黑
					r += p.r * p.a
					g += p.g * p.a
					b += p.b * p.a
					a += p.a
				}
			}
			alpha := a / cnt
			var rr, gg, bb float64
			if a > 0 {
				rr, gg, bb = r/a, g/a, b/a
			}
			out.SetNRGBA(x, y, color.NRGBA{
				R: clamp8(rr), G: clamp8(gg), B: clamp8(bb), A: clamp8(alpha),
			})
		}
	}
	return out
}

func clamp8(v float64) uint8 {
	if v <= 0 {
		return 0
	}
	if v >= 1 {
		return 255
	}
	return uint8(v*255 + 0.5)
}

// draw 绘制指定输出尺寸的图标。
func draw(n int) *image.NRGBA {
	c := newCanvas(n)

	// 蓝色圆底，略微上移给右下角徽章留空间
	c.disc(0.5, 0.46, 0.465, blueDk)
	c.disc(0.5, 0.46, 0.45, blue)

	// 纸飞机：大三角 + 右下折面
	a := point{0.17, 0.53}
	b := point{0.86, 0.22}
	cc := point{0.56, 0.79}
	m := point{0.47, 0.615}
	c.polygon([]point{a, b, cc}, white)
	c.polygon([]point{m, b, cc}, fold)

	// 硬盘徽章：白底圆 + 金属盘体 + 缝 + 指示灯
	c.disc(0.765, 0.765, 0.255, shadow)
	c.disc(0.755, 0.755, 0.248, white)
	c.roundRect(0.605, 0.672, 0.905, 0.842, 0.038, metal)
	c.roundRect(0.605, 0.672, 0.905, 0.727, 0.030, metalL)
	c.roundRect(0.638, 0.752, 0.872, 0.768, 0.008, slot)
	c.disc(0.862, 0.802, 0.020, led)

	return c.image(n)
}

// bmpBytes 把一个图像编码成 ICO 内嵌的 32 位 BMP（DIB）数据。
//
// ICO 里的 BMP 高度要写成两倍：上半是 XOR 像素，下半是 1bpp 的 AND 掩码。
func bmpBytes(im *image.NRGBA) []byte {
	w := im.Bounds().Dx()
	h := im.Bounds().Dy()
	var buf bytes.Buffer
	// BITMAPINFOHEADER
	binary.Write(&buf, binary.LittleEndian, uint32(40))
	binary.Write(&buf, binary.LittleEndian, int32(w))
	binary.Write(&buf, binary.LittleEndian, int32(h*2))
	binary.Write(&buf, binary.LittleEndian, uint16(1))
	binary.Write(&buf, binary.LittleEndian, uint16(32))
	binary.Write(&buf, binary.LittleEndian, uint32(0)) // BI_RGB
	binary.Write(&buf, binary.LittleEndian, uint32(0)) // 图像大小，BI_RGB 可为 0
	binary.Write(&buf, binary.LittleEndian, int32(0))
	binary.Write(&buf, binary.LittleEndian, int32(0))
	binary.Write(&buf, binary.LittleEndian, uint32(0))
	binary.Write(&buf, binary.LittleEndian, uint32(0))
	// XOR：自下而上的 BGRA
	for y := h - 1; y >= 0; y-- {
		for x := 0; x < w; x++ {
			px := im.NRGBAAt(x, y)
			buf.Write([]byte{px.B, px.G, px.R, px.A})
		}
	}
	// AND 掩码：32 位图像靠 alpha 通道，掩码全 0 即可
	rowBytes := ((w + 31) / 32) * 4
	buf.Write(make([]byte, rowBytes*h))
	return buf.Bytes()
}

// writeICO 写出多尺寸 ICO 文件。
func writeICO(path string, imgs []*image.NRGBA) error {
	var buf bytes.Buffer
	binary.Write(&buf, binary.LittleEndian, uint16(0)) // reserved
	binary.Write(&buf, binary.LittleEndian, uint16(1)) // type=icon
	binary.Write(&buf, binary.LittleEndian, uint16(len(imgs)))

	datas := make([][]byte, len(imgs))
	for i, im := range imgs {
		datas[i] = bmpBytes(im)
	}
	offset := 6 + 16*len(imgs)
	for i, im := range imgs {
		w := im.Bounds().Dx()
		h := im.Bounds().Dy()
		bw, bh := byte(w), byte(h)
		if w >= 256 {
			bw = 0
		}
		if h >= 256 {
			bh = 0
		}
		buf.Write([]byte{bw, bh, 0, 0})
		binary.Write(&buf, binary.LittleEndian, uint16(1))
		binary.Write(&buf, binary.LittleEndian, uint16(32))
		binary.Write(&buf, binary.LittleEndian, uint32(len(datas[i])))
		binary.Write(&buf, binary.LittleEndian, uint32(offset))
		offset += len(datas[i])
	}
	for _, d := range datas {
		buf.Write(d)
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

func main() {
	sizes := []int{16, 24, 32, 48, 64, 128, 256}
	imgs := make([]*image.NRGBA, 0, len(sizes))
	for _, s := range sizes {
		imgs = append(imgs, draw(s))
	}
	dir := "assets"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		panic(err)
	}
	if err := writeICO(filepath.Join(dir, "icon.ico"), imgs); err != nil {
		panic(err)
	}
	f, err := os.Create(filepath.Join(dir, "icon.png"))
	if err != nil {
		panic(err)
	}
	defer f.Close()
	if err := png.Encode(f, draw(256)); err != nil {
		panic(err)
	}
}
