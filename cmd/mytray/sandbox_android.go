//go:build android

package main

// 安卓启动准备：把工作目录切到 app 私有目录。
//
// 输入：无
// 输出：切换成功返回 nil；拿不到沙盒路径或切换失败返回错误
// 预期：config.toml / secrets/ / data/ 这些相对路径随之落到沙盒里，
// 安卓上进程 cwd 是只读的 /，不切换则建不出任何运行时文件

import (
	"errors"
	"os"

	"github.com/wailsapp/wails/v3/pkg/application"
)

func prepareSandbox() error {
	dir := application.Android.StoragePath()
	if dir == "" {
		return errors.New("android: storage path unavailable")
	}
	return os.Chdir(dir)
}

// setupAllowed 为 true：没有凭证时开登录面板而不是直接报错。
const setupAllowed = true
