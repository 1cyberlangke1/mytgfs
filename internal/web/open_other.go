//go:build !windows && !darwin && !linux

package web

// 其它平台没有统一的「打开目录」入口，直接报错而不是假装成功。

import "errors"

func openPath(string) error {
	return errors.New("open directory is not supported on this platform")
}
