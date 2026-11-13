//go:build ignore

// PWA 图标生成器：蓝底圆角方块 + 云 + 下载箭头。
//
// 输入：无（尺寸与配色写死在常量里）
// 输出：static/icon-192.png、static/icon-512.png、static/apple-touch-icon.png
// 预期：go run gen/main.go 覆盖生成；4 倍超采样后降采样，不依赖第三方绘图库。
// 图形全部落在中心 80% 安全区内，同一个 512 图可直接当 maskable 用。
package main

import (
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
	bgTop = rgba{0.22, 0.71, 0.94, 1} // 顶部亮蓝
	bgBot = rgba{0.09, 0.50, 0.71, 1} // 底部深蓝
	white = rgba{1, 1, 1, 1}
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

// px 把归一化坐标换算成画布像素中心。
func (c *canvas) px(v float64) float64 { return v * float64(c.w) }

// fill 遍历归一化包围盒，inside 为真的像素填色。
func (c *canvas) fill(x0, y0, x1, y1 float64, col rgba, inside func(x, y float64) bool) {
	ix0, iy0 := int(c.px(x0)), int(c.px(y0))
	ix1, iy1 := int(c.px(x1))+1, int(c.px(y1))+1
	for y := iy0; y < iy1; y++ {
		cy := (float64(y) + 0.5) / float64(c.w)
		for x := ix0; x < ix1; x++ {
			cx := (float64(x) + 0.5) / float64(c.w)
			if inside(cx, cy) {
				c.set(x, y, col)
			}
		}
	}
}

// disc 填充实心圆。
func (c *canvas) disc(cx, cy, r float64, col rgba) {
	c.fill(cx-r, cy-r, cx+r, cy+r, col, func(x, y float64) bool {
		dx, dy := x-cx, y-cy
		return dx*dx+dy*dy <= r*r
	})
}

// insideRoundRect 判断点是否落在圆角矩形内。
//
// 做法：把点夹到内缩矩形的最近点，距离超过半径就在圆角外。
func insideRoundRect(x, y, x0, y0, x1, y1, r float64) bool {
	if x < x0 || x > x1 || y < y0 || y > y1 {
		return false
	}
	nx := math.Min(math.Max(x, x0+r), x1-r)
	ny := math.Min(math.Max(y, y0+r), y1-r)
	dx, dy := x-nx, y-ny
	return dx*dx+dy*dy <= r*r
}

// roundRect 填充圆角矩形，r 是圆角半径。
func (c *canvas) roundRect(x0, y0, x1, y1, r float64, col rgba) {
	c.fill(x0, y0, x1, y1, col, func(x, y float64) bool {
		return insideRoundRect(x, y, x0, y0, x1, y1, r)
	})
}

// tri 填充三角形（重心坐标同号判定）。
func (c *canvas) tri(ax, ay, bx, by, cx, cy float64, col rgba) {
	minX, maxX := math.Min(ax, math.Min(bx, cx)), math.Max(ax, math.Max(bx, cx))
	minY, maxY := math.Min(ay, math.Min(by, cy)), math.Max(ay, math.Max(by, cy))
	cross := func(x0, y0, x1, y1, x2, y2 float64) float64 {
		return (x1-x0)*(y2-y0) - (y1-y0)*(x2-x0)
	}
	c.fill(minX, minY, maxX, maxY, col, func(x, y float64) bool {
		d1 := cross(ax, ay, bx, by, x, y)
		d2 := cross(bx, by, cx, cy, x, y)
		d3 := cross(cx, cy, ax, ay, x, y)
		hasNeg := d1 < 0 || d2 < 0 || d3 < 0
		hasPos := d1 > 0 || d2 > 0 || d3 > 0
		return !(hasNeg && hasPos)
	})
}

// background 画圆角方块 + 纵向渐变。
//
// 输入：无
// 输出：无（直接写画布）
// 预期：渐变按 y 线性插值；圆角外保持全透明，方便系统按任意形状裁切
func (c *canvas) background() {
	const r = 0.22
	for y := 0; y < c.h; y++ {
		cy := (float64(y) + 0.5) / float64(c.h)
		col := rgba{
			r: bgTop.r + (bgBot.r-bgTop.r)*cy,
			g: bgTop.g + (bgBot.g-bgTop.g)*cy,
			b: bgTop.b + (bgBot.b-bgTop.b)*cy,
			a: 1,
		}
		for x := 0; x < c.w; x++ {
			cx := (float64(x) + 0.5) / float64(c.w)
			if insideRoundRect(cx, cy, 0.02, 0.02, 0.98, 0.98, r) {
				c.pix[y*c.w+x] = col
			}
		}
	}
}

// glyph 画白色云 + 向下箭头。
func (c *canvas) glyph() {
	// 云：三个圆 + 底部矩形拼成轮廓
	c.disc(0.40, 0.40, 0.125, white)
	c.disc(0.53, 0.335, 0.155, white)
	c.disc(0.665, 0.405, 0.115, white)
	c.roundRect(0.395, 0.395, 0.665, 0.505, 0.05, white)
	// 箭头：竖杆 + 三角头
	c.roundRect(0.478, 0.565, 0.522, 0.685, 0.022, white)
	c.tri(0.425, 0.665, 0.575, 0.665, 0.5, 0.795, white)
}

// downsample 按 ss×ss 方块平均，输出目标尺寸的 RGBA 图。
func (c *canvas) downsample(size int) *image.RGBA {
	out := image.NewRGBA(image.Rect(0, 0, size, size))
	scale := c.w / size
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var sr, sg, sb, sa float64
			for dy := 0; dy < scale; dy++ {
				for dx := 0; dx < scale; dx++ {
					p := c.pix[(y*scale+dy)*c.w+(x*scale+dx)]
					sr += p.r * p.a
					sg += p.g * p.a
					sb += p.b * p.a
					sa += p.a
				}
			}
			n := float64(scale * scale)
			a := sa / n
			var out1 color.RGBA
			if sa > 0 {
				out1 = color.RGBA{
					R: clamp8(sr / sa * 255),
					G: clamp8(sg / sa * 255),
					B: clamp8(sb / sa * 255),
					A: clamp8(a * 255),
				}
			}
			out.SetRGBA(x, y, out1)
		}
	}
	return out
}

func clamp8(v float64) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(math.Round(v))
}

func main() {
	base := newCanvas(512)
	base.background()
	base.glyph()

	dir := "static"
	for _, item := range []struct {
		name string
		size int
	}{
		{"icon-192.png", 192},
		{"icon-512.png", 512},
		{"apple-touch-icon.png", 180},
	} {
		f, err := os.Create(filepath.Join(dir, item.name))
		if err != nil {
			panic(err)
		}
		if err := png.Encode(f, base.downsample(item.size)); err != nil {
			f.Close()
			panic(err)
		}
		if err := f.Close(); err != nil {
			panic(err)
		}
	}
}
