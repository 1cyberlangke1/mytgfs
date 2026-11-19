package web

// 缓存清理：缩略图/封面缓存按卷分目录存放，这里提供删除文件、
// 改名、删卷时的定向清理，以及旧版 flat 布局的启动清扫。

import (
	"os"
	"path/filepath"
	"sync"
	"time"
)

// sweptDirs 记录已经清扫过的缓存根目录，避免同一进程重复起清扫协程。
var (
	sweptMu   sync.Mutex
	sweptDirs = map[string]struct{}{}
)

// sweepThumbsOnce 对同一目录只启动一次后台清扫。
//
// 输入：dir 缓存根目录
// 输出：无
// 预期：NewHandler 被多次调用（测试、重建 handler）也只扫一次
func sweepThumbsOnce(dir string) {
	sweptMu.Lock()
	if _, done := sweptDirs[dir]; done {
		sweptMu.Unlock()
		return
	}
	sweptDirs[dir] = struct{}{}
	sweptMu.Unlock()
	go SweepFlatLegacy(dir, 24*time.Hour)
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
