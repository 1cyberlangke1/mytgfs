//go:build integration && windows

package vfs

// 资源管理器文件夹视图填充路径测试（无窗口）。
//
// 输入：环境变量 MYTGFS_TEST_POINT，已挂载的盘符（例如 "Y:"）
// 输出：通过 / 失败
// 预期：模拟 Explorer 填充文件夹视图时走的那条 COM 链：
//       NameSpace -> Self.Path -> 逐项读取 Name / IsFolder / Size / Type。
//       任何一项取不到就判失败；全程不创建任何窗口。

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

const explorerWalkScript = `
$point = '%POINT%'
$sh = New-Object -ComObject Shell.Application
$ns = $sh.NameSpace($point)
if ($ns -eq $null) { Write-Output 'NO_NAMESPACE'; exit 2 }
$selfPath = $ns.Self.Path
if ($selfPath -ne $point) { Write-Output ('SELF_PATH_MISMATCH:' + $selfPath); exit 3 }
$n = 0
foreach ($item in $ns.Items()) {
  $null = $item.Name
  $null = $item.IsFolder
  $null = $item.Size
  $null = $item.Type
  $n++
}
Write-Output ('EXPLORER_WALK_OK items=' + $n)
`

func TestExplorerFolderViewPath(t *testing.T) {
	point := os.Getenv("MYTGFS_TEST_POINT")
	if point == "" {
		t.Skip("set MYTGFS_TEST_POINT (e.g. Y:) to run")
	}
	if !strings.HasSuffix(point, ":") {
		t.Fatalf("MYTGFS_TEST_POINT 不是盘符：%q", point)
	}

	script := strings.ReplaceAll(explorerWalkScript, "%POINT%", point+`\`)
	out, err := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script).CombinedOutput()
	if err != nil {
		t.Fatalf("Explorer 文件夹视图路径失败：%v\n%s", err, out)
	}
	if !strings.Contains(string(out), "EXPLORER_WALK_OK") {
		t.Fatalf("Explorer 文件夹视图路径失败：%s", out)
	}
	t.Logf("Explorer 路径正常：%s", strings.TrimSpace(string(out)))
}
