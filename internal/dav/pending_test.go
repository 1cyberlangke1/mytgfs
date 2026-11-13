package dav

// 待补传记账单测：内容写完整后先在索引里记一笔，提交崩溃后能按账恢复。
// 边界：只处理本程序记下的 pending 记录，绝不扫描或删除频道里任何别的消息。

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"mytgfs/internal/index"
)

const helloSHA = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"

// TestFailedUploadKeepsPendingAndTemp 上传失败时保留账本与临时文件，供重启补传。
func TestFailedUploadKeepsPendingAndTemp(t *testing.T) {
	fsys, st, done := newTestFS(t)
	defer done()
	st.uploadErr = errors.New("network down")

	f, err := fsys.OpenFile(context.Background(), "/a.bin", os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err == nil {
		t.Fatal("上传失败时 Close 应返回错误")
	}

	rows, err := fsys.idx.ListPending(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Path != "/a.bin" || rows[0].Size != 5 {
		t.Fatalf("失败应保留一条 pending，实际 %+v", rows)
	}
	if _, err := os.Stat(rows[0].Temp); err != nil {
		t.Fatalf("失败应保留临时文件: %v", err)
	}
}

// TestSuccessfulUploadClearsPending 上传成功后账本清空、临时文件删除。
func TestSuccessfulUploadClearsPending(t *testing.T) {
	fsys, _, done := newTestFS(t)
	defer done()

	f, err := fsys.OpenFile(context.Background(), "/a.bin", os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.Write([]byte("hello"))
	name := f.(*file).tmp.Name()
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	rows, _ := fsys.idx.ListPending(context.Background())
	if len(rows) != 0 {
		t.Fatalf("成功后 pending 应为空，实际 %+v", rows)
	}
	if _, err := os.Stat(name); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("成功后临时文件应删除: %v", err)
	}
}

// TestResumePendingUploadsAndIndexes 重启后按账本把未提交文件补传并登记索引。
func TestResumePendingUploadsAndIndexes(t *testing.T) {
	idx, err := index.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	// 模拟上次崩溃：临时文件完整、账本有一笔，但没上传、没索引
	tmp := filepath.Join(t.TempDir(), ".mytgfs-dav-crash")
	if err := os.WriteFile(tmp, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := idx.PutPending(ctx, index.PendingUpload{Path: "/a.bin", Temp: tmp, Size: 5, Mtime: 1}); err != nil {
		t.Fatal(err)
	}

	st := &fakeStore{}
	fsys := New(ctx, idx, st, 1<<20)
	if err := fsys.ResumePending(); err != nil {
		t.Fatal(err)
	}

	if len(st.uploadKeys) != 1 || st.uploadKeys[0] != helloSHA {
		t.Fatalf("应按内容 sha 补传，实际 %v", st.uploadKeys)
	}
	entry, ok, err := idx.Get(ctx, "/a.bin")
	if err != nil || !ok || entry.Size != 5 || entry.Sha256 != helloSHA {
		t.Fatalf("补传后应登记索引: ok=%v err=%v entry=%+v", ok, err, entry)
	}
	rows, _ := idx.ListPending(ctx)
	if len(rows) != 0 {
		t.Fatalf("补传成功后账本应清空，实际 %+v", rows)
	}
	if _, err := os.Stat(tmp); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("补传成功后临时文件应删除: %v", err)
	}
}

// TestResumePendingDropsMissingTemp 临时文件已不存在（被清扫/损坏）时只清账，不上传。
func TestResumePendingDropsMissingTemp(t *testing.T) {
	idx, err := index.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()

	ctx := context.Background()
	gone := filepath.Join(t.TempDir(), "no-such-temp")
	if err := idx.PutPending(ctx, index.PendingUpload{Path: "/gone.bin", Temp: gone, Size: 5, Mtime: 1}); err != nil {
		t.Fatal(err)
	}

	st := &fakeStore{}
	fsys := New(ctx, idx, st, 1<<20)
	if err := fsys.ResumePending(); err != nil {
		t.Fatal(err)
	}

	if len(st.uploadKeys) != 0 {
		t.Fatalf("临时文件缺失时不应上传，实际 %v", st.uploadKeys)
	}
	rows, _ := idx.ListPending(ctx)
	if len(rows) != 0 {
		t.Fatalf("无法恢复的账本应清掉，实际 %+v", rows)
	}
}
