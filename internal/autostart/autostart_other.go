//go:build !windows && !linux && !darwin

package autostart

// 其它平台（FreeBSD 等）暂不支持开机自启，显式返回错误。

// Enable 在未支持平台返回 ErrUnsupported。
func Enable(string) error { return ErrUnsupported }

// Disable 在未支持平台返回 ErrUnsupported。
func Disable() error { return ErrUnsupported }

// Enabled 在未支持平台返回 false 与 ErrUnsupported。
func Enabled() (bool, error) { return false, ErrUnsupported }
