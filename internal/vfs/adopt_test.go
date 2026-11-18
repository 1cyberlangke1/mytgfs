package vfs

// 接管临时文件：面板分片上传提交后不再整文件拷一份。

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// writeTemp 造一个内容为 body 的临时文件。
func writeTemp(t *testing.T, body string) (string, string) {
	t.Helper()
	name := filepath.Join(t.TempDir(), "payload.bin")
	if err := os.WriteFile(name, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(body))
	return name, hex.EncodeToString(sum[:])
}

// TestAdoptRegistersNode 接管后节点登记完成，临时文件被上传执行体删除。
//
// 输入：临时文件 "hello"
// 输出：/a.txt 节点带同样 sha 与大小；临时文件不存在
// 预期：上传走同一份 uploadJob，断点键就是传入的摘要
func TestAdoptRegistersNode(t *testing.T) {
	fsys, st, cleanup := newTestFS(t)
	defer cleanup()
	ctx := t.Context()

	temp, sha := writeTemp(t, "hello")
	// 无队列的测试环境里同步执行，任务号固定为 0
	if _, err := fsys.Adopt(ctx, "/a.txt", temp, 5, sha); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	n, ok, err := fsys.idx.NodeByPath(ctx, "/a.txt")
	if err != nil || !ok {
		t.Fatalf("节点未登记: ok=%v err=%v", ok, err)
	}
	if n.Sha256 != sha || n.Size != 5 {
		t.Fatalf("节点元数据不符: %+v", n)
	}
	if len(st.uploadKeys) != 1 || st.uploadKeys[0] != sha {
		t.Fatalf("uploadKeys = %v，期望用传入摘要做断点键", st.uploadKeys)
	}
	if _, err := os.Stat(temp); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("成功后临时文件应被删除: %v", err)
	}
}

// TestAdoptRejectsEmpty 空文件当场失败并丢掉临时文件。
func TestAdoptRejectsEmpty(t *testing.T) {
	fsys, _, cleanup := newTestFS(t)
	defer cleanup()

	temp, sha := writeTemp(t, "")
	if _, err := fsys.Adopt(t.Context(), "/a.txt", temp, 0, sha); err == nil {
		t.Fatal("空文件不该接管成功")
	}
	if _, err := os.Stat(temp); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("空文件应被删除: %v", err)
	}
}

// TestAdoptMissingParent 目标目录不存在时失败，不登记节点。
func TestAdoptMissingParent(t *testing.T) {
	fsys, _, cleanup := newTestFS(t)
	defer cleanup()
	ctx := t.Context()

	temp, sha := writeTemp(t, "hello")
	if _, err := fsys.Adopt(ctx, "/nope/a.txt", temp, 5, sha); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v，期望 os.ErrNotExist", err)
	}
	if _, ok, err := fsys.idx.NodeByPath(ctx, "/nope/a.txt"); err != nil || ok {
		t.Fatalf("不该登记节点: ok=%v err=%v", ok, err)
	}
}
