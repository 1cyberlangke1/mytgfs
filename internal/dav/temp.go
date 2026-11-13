package dav

import (
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mytgfs/internal/i18n"
)

// 上传临时文件：落 data/tmp，异常中断的残留按时间清扫。
//
// 输入：新临时文件请求 / 清扫目录与最大存活时长
// 输出：*os.File / 清理数量
// 预期：文件名带固定前缀，只清自己产生的文件

func (d *FileSystem) newTemp() (*os.File, error) {
	dir := d.tmpDir
	if dir == "" {
		dir = os.TempDir()
	} else if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	d.tmpOnce.Do(func() {
		n, err := SweepTemp(dir, tempMaxAge)
		if err != nil {
			slog.Warn(i18n.T("log.tmp_sweep_failed"), "dir", dir, "err", err)
			return
		}
		if n > 0 {
			slog.Info(i18n.T("log.tmp_sweep"), "count", n, "dir", dir)
		}
	})
	return os.CreateTemp(dir, tempPattern)
}

// SweepTemp 删除目录里过期的 mytgfs 上传临时文件。
//
// 输入：dir 目录；maxAge 保留时长
// 输出：删除的数量；error
// 预期：目录不存在视为没有残留；只处理名字以 .mytgfs-dav- 开头的普通文件
func SweepTemp(dir string, maxAge time.Duration) (int, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	cutoff := time.Now().Add(-maxAge)
	n := 0
	for _, e := range ents {
		if e.IsDir() || !strings.HasPrefix(e.Name(), ".mytgfs-dav-") {
			continue
		}
		info, ierr := e.Info()
		if ierr != nil || info.ModTime().After(cutoff) {
			continue
		}
		if rerr := os.Remove(filepath.Join(dir, e.Name())); rerr == nil {
			n++
		}
	}
	return n, nil
}

// Queue 返回当前队列，nil 表示未启用。
