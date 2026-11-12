package vfs

import (
	"context"
	"fmt"
	"time"

	"github.com/winfsp/cgofuse/fuse"
)

// Mount 挂载文件系统，阻塞直到 ctx 取消或卸载。
//
// 输入：ctx；fs 文件系统；point 挂载点；volname 卷名
// 输出：error
// 预期：ctx 取消时自动 Unmount；WinFsp 未安装时返回错误
func Mount(ctx context.Context, fs *FS, point, volname string) error {
	host := fuse.NewFileSystemHost(fs)
	host.SetCapReaddirPlus(true)

	go func() {
		<-ctx.Done()
		host.Unmount()
	}()

	opts := []string{}
	if volname != "" {
		opts = []string{"-o", "volname=" + volname}
	}
	if !host.Mount(point, opts) {
		return fmt.Errorf("挂载 %s 失败（检查 WinFsp 是否安装、盘符是否被占用）", point)
	}
	return nil
}

// nowUnix 返回当前 Unix 时间戳。
func nowUnix() int64 {
	return time.Now().Unix()
}
