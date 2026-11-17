//go:build android

package web

// Android：没有 shell 与文件管理器进程，「打开目录」不成立。
//
// 输入：dir 绝对路径（安卓下忽略）
// 输出：固定错误
// 预期：面板在安卓隐藏「打开下载目录」，接口仍如实报错而不是假装成功

import "errors"

func openPath(string) error {
	return errors.New("open directory is not supported on Android")
}
