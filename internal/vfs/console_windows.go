//go:build windows

package vfs

// 控制台事件的卸载处理。
//
// WinFsp 的卷在进程被强杀时不会走卸载流程；这里捕获控制台关闭、注销、关机，
// 让进程在退出前主动 Unmount，减少残留盘符。

import (
	"sync"
	"syscall"
)

const (
	ctrlCloseEvent    = 2
	ctrlLogoffEvent   = 5
	ctrlShutdownEvent = 6
)

var (
	kernel32           = syscall.NewLazyDLL("kernel32.dll")
	procSetCtrlHandler = kernel32.NewProc("SetConsoleCtrlHandler")
)

// installConsoleCloseHandler 注册控制台事件回调。
//
// 输入：unmount 卸载函数
// 输出：无
// 预期：CLOSE/LOGOFF/SHUTDOWN 时同步执行一次 unmount；
// 强杀进程（TerminateProcess）不会触发这里
func installConsoleCloseHandler(unmount func()) {
	var once sync.Once
	cb := syscall.NewCallback(func(ctrlType uint32) uintptr {
		switch ctrlType {
		case ctrlCloseEvent, ctrlLogoffEvent, ctrlShutdownEvent:
			once.Do(unmount)
		}
		// 返回 0 交回系统默认处理，进程仍会退出
		return 0
	})
	_, _, _ = procSetCtrlHandler.Call(cb, 1)
}
