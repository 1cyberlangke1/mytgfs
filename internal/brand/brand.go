package brand

// 品牌图标的内嵌副本，供桌面壳与托盘直接使用。
//
// 图标由 internal/brand/gen 从 BrandEmblemSvg 的几何生成，
// 这里只做 //go:embed，不在运行时读盘。

import _ "embed"

// ICO 是 Windows 托盘与窗口图标（多尺寸 PNG 压缩条目）。
//
//go:embed icons/icon-windows.ico
var ICO []byte

// PNG256 是托盘在非 Windows 平台使用的 256 像素图标。
//
//go:embed icons/icon-256.png
var PNG256 []byte

// Large 是窗口与应用图标（1024 像素）。
//
//go:embed icons/icon-1024.png
var Large []byte
