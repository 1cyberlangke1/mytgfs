package config

// Downloads 是下载目录配置。
//
// 面板「下载」页把卷内文件直接写到这个目录，并从这里列出已下载文件。
// 留空时使用 data/downloads，保证缺省即可用。

// Downloads 是下载目录设置。
type Downloads struct {
	// Dir 是下载文件落盘的目录；空则用 data/downloads。
	Dir string `toml:"dir"`
}
