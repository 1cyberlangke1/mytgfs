//go:build e2e && !e2eandroid

package web

// forcedE2E 在非安卓 e2e 包里恒为 false。
//
// 输入：无
// 输出：false
// 预期：其它平台只看 MYTGFS_E2E_MOCK 环境变量
func forcedE2E() bool { return false }

