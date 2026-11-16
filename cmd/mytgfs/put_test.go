package main

// collectPutItems 的展开与排序测试：单文件、递归目录、空目录跳过、缺文件报错。

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCollectPutItems 目录递归展开后按远程路径排序，空目录不产生任务。
func TestCollectPutItems(t *testing.T) {
	t.Cleanup(func() {
		globalDir = ""
	})
	t.Setenv("MYTGFS_DIR", "")

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"a.txt", filepath.Join("sub", "b.txt")} {
		if err := os.WriteFile(filepath.Join(root, p), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// 单文件：远程路径拼在 --remote 之后
	one, err := collectPutItems([]string{filepath.Join(root, "a.txt")}, "/base")
	if err != nil {
		t.Fatalf("单文件: %v", err)
	}
	if len(one) != 1 || one[0].remote != "/base/a.txt" {
		t.Fatalf("单文件结果 = %+v", one)
	}
	if one[0].local != filepath.Join(root, "a.txt") {
		t.Fatalf("本地路径 = %q", one[0].local)
	}

	// 递归目录：只收普通文件，按远程路径升序
	tree, err := collectPutItems([]string{root}, "/base")
	if err != nil {
		t.Fatalf("目录: %v", err)
	}
	want := []string{"/base/a.txt", "/base/sub/b.txt"}
	if len(tree) != len(want) {
		t.Fatalf("目录展开 = %+v，期望 %v", tree, want)
	}
	for i, r := range want {
		if tree[i].remote != r {
			t.Errorf("tree[%d] = %q，期望 %q", i, tree[i].remote, r)
		}
	}

	// 不存在的本地路径直接报错
	if _, err := collectPutItems([]string{filepath.Join(root, "absent")}, "/base"); err == nil {
		t.Fatal("缺少本地文件时应返回错误")
	}
}
