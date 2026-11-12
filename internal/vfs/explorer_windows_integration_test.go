//go:build integration && windows

package vfs

// 资源管理器 Shell 绑定可用性集成测试（无窗口）。
//
// 输入：环境变量 MYTGFS_TEST_POINT，已挂载的盘符（例如 "Y:"）
// 输出：通过 / 失败
// 预期：用 SHCreateItemFromParsingName 在后台把该盘符绑定成 IShellItem，
//       这是资源管理器打开文件夹走的同一套 Shell 绑定路径；
//       绑定失败即判不合格，且全程不弹出任何窗口。

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// shellBindScript 在 PowerShell 里内联 C#，只调用 Shell API，不创建窗口。
const shellBindScript = `
Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
public static class ShellBind {
    [DllImport("shell32.dll", CharSet = CharSet.Unicode)]
    static extern int SHCreateItemFromParsingName(string pszPath, IntPtr pbc, ref Guid riid, out IntPtr ppv);

    // CanBind 返回 true 表示 Shell 能把这个路径解析成 IShellItem。
    public static bool CanBind(string path) {
        Guid iid = new Guid("43826d1e-e718-42ee-bc55-a1e261c37bfe"); // IShellItem
        IntPtr ptr;
        int hr = SHCreateItemFromParsingName(path, IntPtr.Zero, ref iid, out ptr);
        if (hr != 0) { return false; }
        Marshal.Release(ptr);
        return true;
    }
}
'@
$ns = (New-Object -ComObject Shell.Application).NameSpace('%POINT%')
if ($ns -eq $null) { Write-Output 'SHELL_NAMESPACE_FAIL'; exit 2 }
$count = $ns.Items().Count
if ($count -lt 0) { Write-Output 'SHELL_ENUM_FAIL'; exit 3 }
if (-not [ShellBind]::CanBind('%POINT%')) { Write-Output 'SHELL_BIND_FAIL'; exit 4 }
Write-Output ('SHELL_OK items=' + $count)
`

func TestExplorerCanBindMountPoint(t *testing.T) {
	point := os.Getenv("MYTGFS_TEST_POINT")
	if point == "" {
		t.Skip("set MYTGFS_TEST_POINT (e.g. Y:) to run")
	}
	if !strings.HasSuffix(point, ":") {
		t.Fatalf("MYTGFS_TEST_POINT 不是盘符：%q", point)
	}

	script := strings.ReplaceAll(shellBindScript, "%POINT%", point+`\`)
	out, err := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script).CombinedOutput()
	if err != nil {
		t.Fatalf("资源管理器 Shell 无法绑定 %s：%v\n%s", point, err, out)
	}
	if !strings.Contains(string(out), "SHELL_OK") {
		t.Fatalf("资源管理器 Shell 无法枚举 %s：%s", point, out)
	}
	t.Logf("资源管理器 Shell 绑定与枚举正常：%s", strings.TrimSpace(string(out)))
}
