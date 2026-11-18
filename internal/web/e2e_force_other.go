//go:build !(e2e && e2eandroid)

package web

// forcedE2E 在非安卓 e2e 包里恒为 false。
//
// 输入：无
// 输出：false
// 预期：其它平台只看 MYTGFS_E2E_MOCK 环境变量
//
//lint:ignore U1000 唯一调用点 e2e_on.go 受 //go:build e2e 约束
func forcedE2E() bool { return false }
