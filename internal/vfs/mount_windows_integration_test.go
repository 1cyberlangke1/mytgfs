//go:build integration && windows

package vfs

// 已挂载盘符的真实文件操作集成测试。
//
// 输入：环境变量 MYTGFS_TEST_POINT，已挂载的盘符（例如 "Y:"）
// 输出：通过 / 失败
// 预期：在挂载点上完成 写→读回→改名→删除 的完整回合，
//       验证 Win32 层（资源管理器列文件、程序读写都走这一层）可用。
// 注意：本测试不验证资源管理器窗口本身，只验证底层挂载可用。

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMountedDriveFileRoundTrip(t *testing.T) {
	point := os.Getenv("MYTGFS_TEST_POINT")
	if point == "" {
		t.Skip("set MYTGFS_TEST_POINT (e.g. Y:) to run")
	}
	root := point + `\`

	const content = "mytgfs round trip"
	src := filepath.Join(root, "it_roundtrip.txt")
	dst := filepath.Join(root, "it_roundtrip_renamed.txt")

	// 1. 写
	if err := os.WriteFile(src, []byte(content), 0o666); err != nil {
		t.Fatalf("写入失败：%v", err)
	}
	t.Cleanup(func() {
		_ = os.Remove(src)
		_ = os.Remove(dst)
	})

	// 2. 读回
	got, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("读回失败：%v", err)
	}
	if string(got) != content {
		t.Fatalf("内容不一致：want %q, got %q", content, got)
	}

	// 3. 列目录能看到
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("列目录失败：%v", err)
	}
	found := false
	for _, e := range entries {
		if e.Name() == filepath.Base(src) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("列目录找不到 %s", filepath.Base(src))
	}

	// 4. 改名
	if err := os.Rename(src, dst); err != nil {
		t.Fatalf("改名失败：%v", err)
	}
	if _, err := os.Stat(dst); err != nil {
		t.Fatalf("改名后找不到新名字：%v", err)
	}

	// 5. 删除
	if err := os.Remove(dst); err != nil {
		t.Fatalf("删除失败：%v", err)
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Fatalf("删除后仍然存在：%v", err)
	}
}
