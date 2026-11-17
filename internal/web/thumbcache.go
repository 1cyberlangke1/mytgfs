package web

// 缓存清理：缩略图/封面缓存按卷分目录存放，这里提供删除文件、
// 改名、删卷时的定向清理，以及旧版 flat 布局的启动清扫。

import (
	"os"
	"path/filepath"
	"time"
)

// PurgeEntry 删掉卷内一个路径的全部缓存文件（缩略图/封面，所有版本）。
//
// 输入：dir 缓存根目录；vol 卷名；p 卷内路径
// 输出：清理掉的文件数；dir 为空表示缓存未启用，返回 0
// 预期：文件删除/改名/移动后调用，孤儿缓存不再堆积
func PurgeEntry(dir, vol, p string) int {
	return (&thumbCache{dir: dir}).purgeEntry(vol, p)
}

// SweepFlatLegacy 清理旧版 flat 布局的残留文件。
//
// 旧版本把缓存文件直接放在 thumbs 根目录，新布局按卷分目录；
// 启动时扫一次，删除根目录下超过 maxAge 的普通文件。
// 输入：dir 缓存根目录；maxAge 文件年龄上限
// 输出：清理掉的文件数
// 预期：子目录不动；目录不存在视为无残留
func SweepFlatLegacy(dir string, maxAge time.Duration) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	cutoff := time.Now().Add(-maxAge)
	n := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		if os.Remove(filepath.Join(dir, e.Name())) == nil {
			n++
		}
	}
	return n
}
