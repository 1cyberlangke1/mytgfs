//go:build e2e && e2eandroid

package web

// forcedE2E 在安卓 e2e 包里恒为 true。
//
// 输入：无
// 输出：true
// 预期：安卓进程拿不到宿主环境变量，改由编译期标签决定挂假数据层
func forcedE2E() bool { return true }
