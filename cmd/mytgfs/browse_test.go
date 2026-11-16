package main

// tree / du 递归逻辑的单测：用内存假目录树验证层数截断与体积汇总。

import (
	"context"
	"io"
	"testing"
	"time"

	"mytgfs/internal/fm"
	"mytgfs/internal/vfs"
)

// fakeFiles 是只实现 List 的 fm.Files 假实现。
//
// 输入：tree 字段里的「目录路径 → 目录项」
// 输出：List 按路径返回目录项
// 预期：其余方法不会被 tree/du 调用，直接 panic 暴露意外依赖
type fakeFiles struct {
	tree map[string][]fm.Entry
}

func (f *fakeFiles) List(_ context.Context, dir string) ([]fm.Entry, error) {
	return f.tree[dir], nil
}

func (f *fakeFiles) Search(context.Context, string, string, int) ([]fm.SearchHit, error) {
	panic("unexpected Search")
}

func (f *fakeFiles) Info(context.Context, string) (fm.Entry, error) {
	panic("unexpected Info")
}

func (f *fakeFiles) Mkdir(context.Context, string) error { panic("unexpected Mkdir") }

func (f *fakeFiles) Upload(context.Context, string, io.Reader) error { panic("unexpected Upload") }

func (f *fakeFiles) AdoptUpload(context.Context, string, string, int64, string) error {
	panic("unexpected AdoptUpload")
}

func (f *fakeFiles) Remove(context.Context, string) error { panic("unexpected Remove") }

func (f *fakeFiles) Move(context.Context, string, string) error { panic("unexpected Move") }

func (f *fakeFiles) Open(context.Context, string) (vfs.File, error) { panic("unexpected Open") }

// testTree 构造 /root + sub + sub/deep 三层假目录。
func testTree() *fakeFiles {
	return &fakeFiles{tree: map[string][]fm.Entry{
		"/root": {
			{Name: "a.txt", Size: 10, Mod: time.Unix(0, 0)},
			{Name: "sub", Dir: true},
		},
		"/root/sub": {
			{Name: "b.bin", Size: 20},
			{Name: "deep", Dir: true},
		},
		"/root/sub/deep": {
			{Name: "c.txt", Size: 5},
		},
	}}
}

// TestWalkTree 层数不限时构造整棵树，目录体积等于子项合计。
func TestWalkTree(t *testing.T) {
	n, err := walkTree(context.Background(), testTree(), "/root", "root", 0, 0)
	if err != nil {
		t.Fatalf("walkTree: %v", err)
	}
	if !n.Dir || n.Path != "/root" || n.Size != 35 {
		t.Fatalf("根节点 = %+v", n)
	}
	if len(n.Children) != 2 || n.Children[0].Name != "a.txt" || n.Children[1].Name != "sub" {
		t.Fatalf("根子项 = %+v", n.Children)
	}
	sub := n.Children[1]
	if sub.Size != 25 || len(sub.Children) != 2 {
		t.Fatalf("sub = %+v", sub)
	}
	deep := sub.Children[1]
	if deep.Path != "/root/sub/deep" || deep.Size != 5 || len(deep.Children) != 1 {
		t.Fatalf("deep = %+v", deep)
	}
}

// TestWalkTreeDepth 层数截断后不再展开更深的子目录。
func TestWalkTreeDepth(t *testing.T) {
	n, err := walkTree(context.Background(), testTree(), "/root", "root", 0, 1)
	if err != nil {
		t.Fatalf("walkTree: %v", err)
	}
	sub := n.Children[1]
	if !sub.Dir || len(sub.Children) != 0 || sub.Size != 0 {
		t.Fatalf("--depth 1 时 sub 应被截断: %+v", sub)
	}
	if n.Size != 10 {
		t.Fatalf("截断后根体积 = %d，期望 10（只有 a.txt）", n.Size)
	}
}

// TestWalkDu 后序汇总：子目录先输出，父目录包含整棵子树。
func TestWalkDu(t *testing.T) {
	var entries []duEntry
	size, files, err := walkDu(context.Background(), testTree(), "/root", &entries)
	if err != nil {
		t.Fatalf("walkDu: %v", err)
	}
	if size != 35 || files != 3 {
		t.Fatalf("合计 = %d 字节 / %d 文件，期望 35 / 3", size, files)
	}
	want := []duEntry{
		{Path: "/root/sub/deep", Files: 1, Size: 5},
		{Path: "/root/sub", Files: 2, Size: 25},
		{Path: "/root", Files: 3, Size: 35},
	}
	if len(entries) != len(want) {
		t.Fatalf("entries = %+v", entries)
	}
	for i := range want {
		if entries[i] != want[i] {
			t.Errorf("entries[%d] = %+v，期望 %+v", i, entries[i], want[i])
		}
	}
}
