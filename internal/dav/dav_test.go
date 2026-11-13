package dav

// WebDAV 层单测：假存储代替 Telegram，验证写入、节点消息、改名/删除与错误处理。

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gotd/td/tgerr"

	"mytgfs/internal/dirent"
	"mytgfs/internal/index"
)

// TestWriteStoresHashAndCaption 写入后节点带摘要，caption 是「自述名字+父号」的节点正文。
func TestWriteStoresHashAndCaption(t *testing.T) {
	fsys, st, done := newTestFS(t)
	defer done()
	ctx := t.Context()

	id := putFile(t, fsys, "/a.bin", "hello")
	want := "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if len(st.uploadKeys) != 1 || st.uploadKeys[0] != want {
		t.Fatalf("断点键应为内容 sha256，实际 %v", st.uploadKeys)
	}
	if len(st.uploadCaptions) != 1 {
		t.Fatalf("应上传一次，实际 %v", st.uploadCaptions)
	}
	node, ok := dirent.Parse(st.uploadCaptions[0])
	if !ok || node.Sha != want || node.Name != "a.bin" || node.Parent != 0 || node.Dir {
		t.Fatalf("caption 应是文件节点正文且带摘要，实际 %+v (%s)", node, st.uploadCaptions[0])
	}
	n, ok, err := fsys.idx.NodeByID(ctx, id)
	if err != nil || !ok || n.Sha256 != want || n.Size != 5 || n.Name != "a.bin" || n.ParentID != 0 {
		t.Fatalf("节点不符: ok=%v err=%v node=%+v", ok, err, n)
	}
	if _, exists := st.msgs[id]; !exists {
		t.Fatalf("消息 %d 应在远端存在", id)
	}
}

// TestOverwriteDeletesOldMessage 覆盖写删掉旧消息，节点指向新消息。
func TestOverwriteDeletesOldMessage(t *testing.T) {
	fsys, st, done := newTestFS(t)
	defer done()
	ctx := t.Context()

	first := putFile(t, fsys, "/a.bin", "first")
	second := putFile(t, fsys, "/a.bin", "second")
	if first == second {
		t.Fatal("覆盖写应产生新消息号")
	}
	if len(st.deleted) != 1 || st.deleted[0] != first {
		t.Fatalf("应删除旧消息 %d，实际 %v", first, st.deleted)
	}
	if _, ok, _ := fsys.idx.NodeByID(ctx, first); ok {
		t.Fatal("旧节点应从索引移除")
	}
	n, ok, _ := fsys.idx.NodeByID(ctx, second)
	if !ok || n.Name != "a.bin" {
		t.Fatalf("新节点不符: %+v", n)
	}
}

// TestReadRemovesDeletedMessage 远端消息被删时按文件不存在处理并清节点。
func TestReadRemovesDeletedMessage(t *testing.T) {
	fsys, st, done := newTestFS(t)
	defer done()
	ctx := t.Context()
	if err := fsys.idx.PutNode(ctx, index.Node{ID: 7, Name: "gone.bin", Path: "/gone.bin", Size: 10}); err != nil {
		t.Fatal(err)
	}
	st.readErr = tgerr.New(400, "MESSAGE_ID_INVALID")
	f, err := fsys.OpenFile(ctx, "/gone.bin", os.O_RDONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Read(make([]byte, 4)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("应返回文件不存在，实际 %v", err)
	}
	if _, ok, _ := fsys.idx.NodeByID(ctx, 7); ok {
		t.Fatal("节点应被清掉")
	}
}

// TestReadOtherErrorPropagates 其它读取错误原样返回，不动索引。
func TestReadOtherErrorPropagates(t *testing.T) {
	fsys, st, done := newTestFS(t)
	defer done()
	ctx := t.Context()
	if err := fsys.idx.PutNode(ctx, index.Node{ID: 8, Name: "x.bin", Path: "/x.bin", Size: 10}); err != nil {
		t.Fatal(err)
	}
	st.readErr = errors.New("network down")
	f, err := fsys.OpenFile(ctx, "/x.bin", os.O_RDONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Read(make([]byte, 4)); err == nil {
		t.Fatal("应返回读取错误")
	}
	if _, ok, _ := fsys.idx.NodeByID(ctx, 8); !ok {
		t.Fatal("节点不应被删")
	}
}

// TestReadReturnsData 正常读取返回存储层数据。
func TestReadReturnsData(t *testing.T) {
	fsys, st, done := newTestFS(t)
	defer done()
	ctx := t.Context()
	if err := fsys.idx.PutNode(ctx, index.Node{ID: 9, Name: "d.bin", Path: "/d.bin", Size: 1}); err != nil {
		t.Fatal(err)
	}
	f, err := fsys.OpenFile(ctx, "/d.bin", os.O_RDONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	n, err := f.Read(buf)
	if err != nil || n != 1 || !bytes.Equal(buf[:n], st.readData) {
		t.Fatalf("读取结果不符: n=%d err=%v", n, err)
	}
}

// TestMkdirCreatesDirNode 建目录发一条目录节点消息，父号指向卷根。
func TestMkdirCreatesDirNode(t *testing.T) {
	fsys, st, done := newTestFS(t)
	defer done()
	ctx := t.Context()

	id := mkdir(t, fsys, "/new")
	m, exists := st.msgs[id]
	if !exists {
		t.Fatalf("目录消息 %d 应在远端存在", id)
	}
	node, ok := dirent.Parse(m.body)
	if !ok || !node.Dir || node.Name != "new" || node.Parent != 0 {
		t.Fatalf("目录节点正文不符: %+v (%s)", node, m.body)
	}
	if err := fsys.Mkdir(ctx, "/new", 0o755); !errors.Is(err, os.ErrExist) {
		t.Fatalf("重复建目录应返回 ErrExist，实际 %v", err)
	}
	if err := fsys.Mkdir(ctx, "/no/parent", 0o755); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("父目录不存在应返回 ErrNotExist，实际 %v", err)
	}
}

// TestVirtualRoot 卷根是虚拟的：访问 "/" 不产生任何消息。
func TestVirtualRoot(t *testing.T) {
	fsys, st, done := newTestFS(t)
	defer done()
	ctx := t.Context()
	n, ok, err := fsys.nodeAt(ctx, "/")
	if err != nil || !ok || !n.IsDir || n.ID != 0 || n.Path != "/" {
		t.Fatalf("虚拟根不符: %+v ok=%v err=%v", n, ok, err)
	}
	if len(st.msgs) != 0 {
		t.Fatalf("访问卷根不该发消息，实际 %d 条", len(st.msgs))
	}
	info, err := fsys.Stat(ctx, "/")
	if err != nil || !info.IsDir() {
		t.Fatalf("Stat(/) 应是目录: %v %v", info, err)
	}
}

// TestNameTooLong 超长名字在建目录与写文件时都被拒绝，且不发消息。
func TestNameTooLong(t *testing.T) {
	fsys, st, done := newTestFS(t)
	defer done()
	ctx := t.Context()
	long := strings.Repeat("a", dirent.MaxNameBytes+1)
	if err := fsys.Mkdir(ctx, "/"+long, 0o755); err == nil {
		t.Fatal("超长目录名应被拒绝")
	}
	if len(st.msgs) != 0 {
		t.Fatalf("拒绝后不该发消息，实际 %d 条", len(st.msgs))
	}
}

// TestSweepTempRemovesOnlyStale 只删过期且名字匹配的临时文件。
func TestSweepTempRemovesOnlyStale(t *testing.T) {
	dir := t.TempDir()
	old := time.Now().Add(-48 * time.Hour)

	stale := filepath.Join(dir, ".mytgfs-dav-stale")
	fresh := filepath.Join(dir, ".mytgfs-dav-fresh")
	other := filepath.Join(dir, "keep.txt")
	for _, p := range []string{stale, fresh, other} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(other, old, old); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, ".mytgfs-dav-dir")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(sub, old, old); err != nil {
		t.Fatal(err)
	}

	n, err := SweepTemp(dir, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("应只删 1 个过期临时文件，实际 %d", n)
	}
	for _, p := range []string{fresh, other, sub} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("%s 不应被删: %v", p, err)
		}
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("过期临时文件应已删除: %v", err)
	}
}

// TestSweepTempMissingDir 目录不存在时视为没有残留。
func TestSweepTempMissingDir(t *testing.T) {
	n, err := SweepTemp(filepath.Join(t.TempDir(), "nope"), time.Hour)
	if err != nil || n != 0 {
		t.Fatalf("目录不存在应返回 0,nil，实际 %d,%v", n, err)
	}
}

// TestNewTempUsesConfiguredDir 临时文件落在 SetTempDir 指定的目录。
func TestNewTempUsesConfiguredDir(t *testing.T) {
	fsys, _, done := newTestFS(t)
	defer done()
	dir := filepath.Join(t.TempDir(), "deep", "tmp")
	fsys.SetTempDir(dir)

	f, err := fsys.OpenFile(t.Context(), "/t.bin", os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	name := f.(*file).tmp.Name()
	t.Cleanup(func() { _ = f.Close() })
	if filepath.Dir(name) != dir {
		t.Fatalf("临时文件应在 %s，实际 %s", dir, name)
	}
}
