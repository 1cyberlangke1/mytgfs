package vfs

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/winfsp/cgofuse/fuse"
)

// Mount 挂载文件系统，阻塞直到 ctx 取消或卸载。
//
// 输入：ctx；fs 文件系统；point 挂载点；volname 卷名
// 输出：error
// 预期：ctx 取消时自动 Unmount；WinFsp 未安装时返回错误
func Mount(ctx context.Context, fs *FS, point, volname string) error {
	defer fs.Close()
	host := fuse.NewFileSystemHost(fs)
	host.SetCapReaddirPlus(true)
	// 目录变更后主动通知资源管理器刷新，否则新文件要按 F5 才出现
	fs.SetNotify(func(p string, action uint32) { host.Notify(p, action) })

	go func() {
		<-ctx.Done()
		host.Unmount()
	}()

	// 关窗口/注销/关机时先卸载，避免留下盘符
	installConsoleCloseHandler(func() { host.Unmount() })

	// 普通盘挂载：进程退出后卷设备随即消失，不会在资源管理器留下位置缓存。
	opts := []string{}
	if name := shareName(volname); name != "" {
		opts = append(opts, "-o", "volname="+name)
	}
	if !host.Mount(point, opts) {
		return fmt.Errorf("挂载 %s 失败（检查 WinFsp 是否安装、盘符是否被占用）", point)
	}
	return nil
}

// shareName 生成用于卷名的名称。
//
// 输入：卷名，可能为空
// 输出：不含反斜杠的名称
// 预期：空名回落到 mytgfs；反斜杠和斜杠替换成短横线
func shareName(volname string) string {
	name := strings.NewReplacer(`\`, "-", "/", "-").Replace(volname)
	if name == "" {
		return "mytgfs"
	}
	return name
}

// nowUnix 返回当前 Unix 时间戳。
func nowUnix() int64 {
	return time.Now().Unix()
}
