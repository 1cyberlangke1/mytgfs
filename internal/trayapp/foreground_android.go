//go:build android

package trayapp

// 安卓后台保活：没有托盘，息屏后进程会被系统回收，用前台服务挂着。

import (
	"encoding/json"

	"github.com/wailsapp/wails/v3/pkg/application"

	"mytgfs/internal/i18n"
)

// startForeground 启动前台服务。
//
// 输入：无
// 输出：无
// 预期：通知栏常驻一条「mytgfs 正在同步」；权限被拒也只是没有通知，
// 服务本体仍能跑，失败只在 logcat 里可见
func startForeground() {
	payload, err := json.Marshal(map[string]string{
		"title": "mytgfs",
		"text":  i18n.T("android.fgs_text"),
	})
	if err != nil {
		return
	}
	application.Android.StartForegroundService(string(payload))
}
