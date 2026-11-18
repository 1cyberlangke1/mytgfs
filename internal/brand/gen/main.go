// 图标生成器：把 demo 的品牌徽标（蓝渐变 + 文件夹 + 纸飞机）渲染成应用与托盘图标。
//
// 输入：无（内嵌 SVG 路径）
// 输出：assets/brand 下的 PNG 与 Windows ICO
// 预期：
//   - 底色渐变由本程序逐像素计算，避免 SVG 渐变在 oksvg 下偏色
//   - 文件夹与纸飞机由同一份矢量路径渲染后叠加
//   - 尺寸 16..1024 全部来自同一份几何，ICO 用 PNG 压缩条目
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/srwiley/oksvg"
	"github.com/srwiley/rasterx"
	"github.com/tc-hib/winres"
)

// plateSVG 只有文件夹底板（描边 + 半透明白填充），坐标系与原 emblem 一致。
const plateSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 40 40" fill="none">
  <path d="M10 14.5C10 12.567 11.567 11 13.5 11H18.2L20.7 13.5H26.5C28.433 13.5 30 15.067 30 17V25.5C30 27.433 28.433 29 26.5 29H13.5C11.567 29 10 27.433 10 25.5V14.5Z" fill="#FFFFFF" fill-opacity="0.18" stroke="white" stroke-opacity="0.55" stroke-width="1.4"/>
</svg>`

// planeSVG 只有纸飞机，纯白。
const planeSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 40 40" fill="none">
  <path d="M25.8 16.2L13.6 20.9C13.0 21.15 13.02 21.95 13.62 22.15L16.75 23.15L23.5 18.8L18.35 24.05V26.8C18.35 27.35 19.02 27.6 19.4 27.2L21.15 25.35L23.9 27.4C24.4 27.78 25.12 27.48 25.25 26.85L26.85 17.15C26.98 16.45 26.42 15.95 25.8 16.2Z" fill="#FFFFFF"/>
</svg>`

// brandStops 是底板渐变的三段颜色与位置。
var brandStops = []struct {
	pos float64
	r   float64
	g   float64
	b   float64
}{
	{0.00, 0x00, 0x7A, 0xFF},
	{0.55, 0x02, 0x84, 0xC7},
	{1.00, 0x06, 0xB6, 0xD4},
}

// lerpStops 按对角位置 t 取渐变色。
func lerpStops(t float64) (float64, float64, float64) {
	if t <= brandStops[0].pos {
		return brandStops[0].r, brandStops[0].g, brandStops[0].b
	}
	for i := 1; i < len(brandStops); i++ {
		a, b := brandStops[i-1], brandStops[i]
		if t <= b.pos {
			k := (t - a.pos) / (b.pos - a.pos)
			return a.r + (b.r-a.r)*k, a.g + (b.g-a.g)*k, a.b + (b.b-a.b)*k
		}
	}
	last := brandStops[len(brandStops)-1]
	return last.r, last.g, last.b
}

// roundedCoverage 返回像素落在圆角矩形内的覆盖率（0..1，简单 1px 抗锯齿）。
func roundedCoverage(x, y, size float64, radius float64) float64 {
	// 距离最近圆角圆心的距离；四角之外才需要判断
	cx := math.Min(math.Max(x, radius), size-radius)
	cy := math.Min(math.Max(y, radius), size-radius)
	d := math.Hypot(x-cx, y-cy)
	if d <= radius-0.5 {
		return 1
	}
	if d >= radius+0.5 {
		return 0
	}
	return radius + 0.5 - d
}

// renderPaths 把一个 SVG 路径渲染成透明底图。
func renderPaths(svg string, size int) (*image.RGBA, error) {
	icon, err := oksvg.ReadIconStream(strings.NewReader(svg), oksvg.WarnErrorMode)
	if err != nil {
		return nil, err
	}
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	icon.SetTarget(0, 0, float64(size), float64(size))
	icon.Draw(rasterx.NewDasher(size, size, rasterx.NewScannerGV(size, size, img, img.Bounds())), 1)
	return img, nil
}

// render 合成最终图标：渐变圆角底 + 文件夹 + 纸飞机。
//
// 输入：size 像素边长
// 输出：RGBA 图；失败返回 error
// 预期：桌面 / 托盘 / 旧版安卓位图共用这一份几何
func render(size int) (image.Image, error) {
	return renderIcon(size, 11.0/40.0*float64(size), true, true)
}

// renderRound 合成圆形底图标（安卓旧版 ic_launcher_round）。
//
// 输入：size 像素边长
// 输出：RGBA 图；失败返回 error
// 预期：底色外接圆内不透明，圆外全透明，避免被系统再裁一次时出现方角
func renderRound(size int) (image.Image, error) {
	return renderIcon(size, float64(size)/2, false, true)
}

// renderBackground 只铺渐变底，不画图案（安卓自适应图标背景层）。
//
// 输入：size 像素边长
// 输出：RGBA 图；失败返回 error
// 预期：整格不透明；图案由前景层叠加
func renderBackground(size int) (image.Image, error) {
	return renderIcon(size, 0, false, false)
}

// renderIcon 合成图标：渐变底 + 文件夹 + 纸飞机。
//
// 输入：size 像素边长；radius 底色圆角半径（0 表示铺满整格）；border 是否描白色内边；
// withShapes 是否叠加文件夹与纸飞机
// 输出：RGBA 图；失败返回 error
// 预期：
//   - 安卓自适应图标的背景层用 radius=0、border=false、withShapes=false，铺满 108dp 画布
//   - 其余尺寸沿用圆角 + 描边，保持与桌面图标一致
func renderIcon(size int, radius float64, border, withShapes bool) (image.Image, error) {
	var plate, plane *image.RGBA
	if withShapes {
		var err error
		if plate, err = renderPaths(plateSVG, size); err != nil {
			return nil, err
		}
		if plane, err = renderPaths(planeSVG, size); err != nil {
			return nil, err
		}
	}

	s := float64(size)
	// 描边带宽：随尺寸缩放，最小 1px，保证 16px 托盘图标也看得出边框
	strokeW := math.Max(1, s*0.012)
	innerRadius := math.Max(radius-strokeW, 0)
	out := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := range size {
		for x := range size {
			fx, fy := float64(x)+0.5, float64(y)+0.5
			// radius<=0 表示铺满整格：roundedCoverage 在半径 0 时会退化成亚像素边，
			// 这里直接给满覆盖率，避免整张自适应图标背景变成半透明白
			cover := 1.0
			if radius > 0 {
				cover = roundedCoverage(fx, fy, s, radius)
			}
			if cover <= 0 {
				continue
			}
			if border {
				// cover-inner = 落在描边带内的比例
				inner := roundedCoverage(fx-strokeW, fy-strokeW, s-2*strokeW, innerRadius)
				if inner < 1 {
					band := math.Min(1, cover-inner)
					// image.RGBA 是预乘 alpha：白色必须写成 R=G=B=A，否则编码时溢出发黑
					a := uint8(205 * band)
					out.SetRGBA(x, y, color.RGBA{R: a, G: a, B: a, A: a})
					continue
				}
			}
			t := (fx + fy) / (2 * s)
			r, g, b := lerpStops(t)
			// 最外 1px 亚像素边：半透明白，避免圆角发毛
			if cover < 1 {
				rim := uint8(64 * cover)
				out.SetRGBA(x, y, color.RGBA{R: rim, G: rim, B: rim, A: rim})
				continue
			}
			out.SetRGBA(x, y, color.RGBA{R: uint8(r), G: uint8(g), B: uint8(b), A: 255})
		}
	}

	if withShapes {
		blitShapes(out, plate, plane, size)
	}
	return out, nil
}

// renderForeground 只画文件夹与纸飞机，透明底（安卓自适应图标前景层）。
//
// 输入：size 像素边长
// 输出：RGBA 图；失败返回 error
// 预期：图案位于画布中心约一半半径内，落在自适应图标的安全区里
func renderForeground(size int) (image.Image, error) {
	plate, err := renderPaths(plateSVG, size)
	if err != nil {
		return nil, err
	}
	plane, err := renderPaths(planeSVG, size)
	if err != nil {
		return nil, err
	}
	out := image.NewRGBA(image.Rect(0, 0, size, size))
	blitShapes(out, plate, plane, size)
	return out, nil
}

// blitShapes 把文件夹（带 alpha）与纸飞机叠到 out 上。
//
// 输入：out 目标图；plate/plane 已渲染的图层；size 边长
// 输出：无（原地修改 out）
// 预期：只影响 alpha>0 的像素，透明区域保持原样
func blitShapes(out, plate, plane *image.RGBA, size int) {
	for y := range size {
		for x := range size {
			if c := plate.RGBAAt(x, y); c.A > 0 {
				blend(out.RGBAAt(x, y), c, out, x, y)
			}
			if c := plane.RGBAAt(x, y); c.A > 0 {
				blend(out.RGBAAt(x, y), c, out, x, y)
			}
		}
	}
}

// blend 把颜色 src 按 alpha 叠到 dst 上并写回。
func blend(dst, src color.RGBA, img *image.RGBA, x, y int) {
	sa := float64(src.A) / 255
	da := float64(dst.A) / 255
	oa := sa + da*(1-sa)
	if oa <= 0 {
		return
	}
	mix := func(s, d uint8) uint8 {
		return uint8((float64(s)*sa + float64(d)*da*(1-sa)) / oa)
	}
	img.SetRGBA(x, y, color.RGBA{R: mix(src.R, dst.R), G: mix(src.G, dst.G), B: mix(src.B, dst.B), A: uint8(oa * 255)})
}

// pngBytes 把图像编码成 PNG。
func pngBytes(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// writeICO 写多尺寸 PNG 压缩的 ICO 文件。
//
// 输入：path 目标路径；sizes 每个条目的边长
// 输出：error
// 预期：目录项宽高为 0 表示 256；数据偏移按头长度累加
func writeICO(path string, sizes []int) error {
	type entry struct {
		size int
		data []byte
	}
	icons := make([]entry, 0, len(sizes))
	for _, s := range sizes {
		img, err := render(s)
		if err != nil {
			return err
		}
		data, err := pngBytes(img)
		if err != nil {
			return err
		}
		icons = append(icons, entry{size: s, data: data})
	}

	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.LittleEndian, uint16(0))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(1))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(len(icons)))

	offset := 6 + 16*len(icons)
	for _, ic := range icons {
		dim := byte(ic.size)
		if ic.size >= 256 {
			dim = 0
		}
		buf.WriteByte(dim)
		buf.WriteByte(dim)
		buf.WriteByte(0)
		buf.WriteByte(0)
		_ = binary.Write(&buf, binary.LittleEndian, uint16(1))
		_ = binary.Write(&buf, binary.LittleEndian, uint16(32))
		_ = binary.Write(&buf, binary.LittleEndian, uint32(len(ic.data)))
		_ = binary.Write(&buf, binary.LittleEndian, uint32(offset))
		offset += len(ic.data)
	}
	for _, ic := range icons {
		buf.Write(ic.data)
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// writeSYSO 把 ICO 编译成 Windows 资源对象，让图标进入 exe 文件本体。
//
// 输入：dst 输出路径；icoPath 多尺寸 ICO
// 输出：写盘错误
// 预期：Go 只会把 .syso 里的资源链接进二进制；application.Options.Icon 只影响
//
//	窗口与托盘，Explorer、快捷方式和任务管理器读的是文件资源里的图标。
//	文件名带 _windows_amd64，只对 windows/amd64 生效
func writeSYSO(dst, icoPath string) error {
	f, err := os.Open(icoPath)
	if err != nil {
		return err
	}
	defer f.Close()
	ico, err := winres.LoadICO(f)
	if err != nil {
		return fmt.Errorf("%s: %w", icoPath, err)
	}
	var rs winres.ResourceSet
	if err := rs.SetIcon(winres.RT_ICON, ico); err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	err = rs.WriteObject(out, winres.ArchAMD64)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	return err
}

// writePNG 把图像编码成 PNG 并写盘。
//
// 输入：path 目标路径；img 图像
// 输出：写盘或编码错误
// 预期：父目录由调用方保证已存在
func writePNG(path string, img image.Image) error {
	data, err := pngBytes(img)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// writeAndroidIcons 生成安卓启动器图标。
//
// 输入：root 安卓 res 目录
// 输出：写盘错误
// 预期：
//   - mipmap-<density>/ic_launcher.png 与 ic_launcher_round.png 给 API<26 的旧系统
//   - 同目录的 ic_launcher_background/foreground.png 加 mipmap-anydpi-v26/*.xml
//     给 API>=26 的自适应图标：背景铺满整格，前景图案落在中间安全区
func writeAndroidIcons(root string) error {
	// 旧版图标：48dp 起的五档位图
	legacy := []struct {
		dir  string
		size int
	}{
		{"mdpi", 48}, {"hdpi", 72}, {"xhdpi", 96}, {"xxhdpi", 144}, {"xxxhdpi", 192},
	}
	// 自适应图标画布是 108dp，前景/背景按同一套密度各出一份
	adaptive := []struct {
		dir  string
		size int
	}{
		{"mdpi", 108}, {"hdpi", 162}, {"xhdpi", 216}, {"xxhdpi", 324}, {"xxxhdpi", 432},
	}

	for _, d := range legacy {
		dir := filepath.Join(root, "mipmap-"+d.dir)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		square, err := render(d.size)
		if err != nil {
			return err
		}
		if err := writePNG(filepath.Join(dir, "ic_launcher.png"), square); err != nil {
			return err
		}
		round, err := renderRound(d.size)
		if err != nil {
			return err
		}
		if err := writePNG(filepath.Join(dir, "ic_launcher_round.png"), round); err != nil {
			return err
		}
	}

	for _, d := range adaptive {
		dir := filepath.Join(root, "mipmap-"+d.dir)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		bg, err := renderBackground(d.size)
		if err != nil {
			return err
		}
		if err := writePNG(filepath.Join(dir, "ic_launcher_background.png"), bg); err != nil {
			return err
		}
		fg, err := renderForeground(d.size)
		if err != nil {
			return err
		}
		if err := writePNG(filepath.Join(dir, "ic_launcher_foreground.png"), fg); err != nil {
			return err
		}
	}

	anydpi := filepath.Join(root, "mipmap-anydpi-v26")
	if err := os.MkdirAll(anydpi, 0o755); err != nil {
		return err
	}
	const layerXML = `<?xml version="1.0" encoding="utf-8"?>
<adaptive-icon xmlns:android="http://schemas.android.com/apk/res/android">
    <background android:drawable="@mipmap/ic_launcher_background" />
    <foreground android:drawable="@mipmap/ic_launcher_foreground" />
</adaptive-icon>
`
	for _, name := range []string{"ic_launcher.xml", "ic_launcher_round.xml"} {
		if err := os.WriteFile(filepath.Join(anydpi, name), []byte(layerXML), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func main() {
	const out = "internal/brand/icons"
	if err := os.MkdirAll(out, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for _, size := range []int{16, 24, 32, 48, 64, 128, 192, 256, 512, 1024} {
		img, err := render(size)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		data, err := pngBytes(img)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		name := filepath.Join(out, fmt.Sprintf("icon-%d.png", size))
		if err := os.WriteFile(name, data, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("%s %d B\n", name, len(data))
	}
	icoPath := filepath.Join(out, "icon-windows.ico")
	if err := writeICO(icoPath, []int{16, 24, 32, 48, 64, 128, 256}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	st, _ := os.Stat(icoPath)
	fmt.Printf("%s %d B\n", icoPath, st.Size())

	// 可执行文件本体也要图标：没有 .syso 时 Explorer 只显示空白文件图标
	for _, dir := range []string{"cmd/mytray", "cmd/mytgfs"} {
		dst := filepath.Join(dir, "rsrc_windows_amd64.syso")
		if err := writeSYSO(dst, icoPath); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		st, _ := os.Stat(dst)
		fmt.Printf("%s %d B\n", dst, st.Size())
	}

	// 安卓启动器图标：旧版位图 + 自适应图标分层
	const androidRes = "build/android/app/src/main/res"
	if err := writeAndroidIcons(androidRes); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("%s/mipmap-*/ic_launcher*.png 已更新\n", androidRes)
}
