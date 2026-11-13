package autostart

// Control 把平台实现包成面板可调用的开关。

import "os"

// Control 实现面板的自启接口，无状态。
//
// 输入：无
// 输出：实现 Enabled() (bool, error) 与 Set(bool) error 的零值类型
// 预期：Set(true) 注册同目录的托盘二进制，登录后不弹控制台
type Control struct{}

// Enabled 返回系统注册项里的状态。
func (Control) Enabled() (bool, error) { return Enabled() }

// Set 写入或清除系统注册项。
//
// 输入：on=true 注册，false 注销
// 输出：写入错误
// 预期：当前进程无法取到自身路径时直接失败，不写半截注册项
func (Control) Set(on bool) error {
	if !on {
		return Disable()
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return Enable(PreferTray(exe))
}
