package config

// Downloads 是下载目录配置。
//
// 面板「下载」页把卷内文件直接写到这个目录，并从这里列出已下载文件。
// 留空时使用 data/downloads，保证缺省即可用。

import "strings"

// DefaultDir 是下载目录的缺省值，相对路径按配置文件所在目录解析。
const DefaultDir = "data/downloads"

// Downloads 是下载目录设置。
type Downloads struct {
	// Dir 是下载文件落盘的目录；空则用 DefaultDir。
	// 绝对路径原样使用，相对路径按程序目录解析。
	Dir string `toml:"dir"`
}

// EffectiveDir 返回实际使用的下载目录（未展开相对路径）。
//
// 输入：无
// 输出：空值时返回 DefaultDir
// 预期：配置解析与面板展示共用同一份默认值，界面不再留空
func (d Downloads) EffectiveDir() string {
	if strings.TrimSpace(d.Dir) == "" {
		return DefaultDir
	}
	return d.Dir
}
