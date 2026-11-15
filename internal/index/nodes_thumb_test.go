package index

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// 回归背景：视频封面利用消息自带的 stripped 缩略图，存进 nodes.thumb BLOB，
// 同步入库后封面展示零网络请求。旧库无此列，Open 时必须自动迁移。
func TestPutNodeThumbRoundtrip(t *testing.T) {
	ctx := t.Context()
	i, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = i.Close() })

	thumb := []byte{0xFF, 0xD8, 0x01, 0x02}
	if err := i.PutNode(ctx, Node{ID: 1, Name: "v.mp4", Thumb: thumb}); err != nil {
		t.Fatal(err)
	}
	n, ok, err := i.NodeByID(ctx, 1)
	if err != nil || !ok {
		t.Fatalf("NodeByID: ok=%v err=%v", ok, err)
	}
	if string(n.Thumb) != string(thumb) {
		t.Fatalf("thumb 未往返: %d bytes", len(n.Thumb))
	}

	// 覆盖写不带 thumb → 列被清空（INSERT OR REPLACE 语义如实）
	if err := i.PutNode(ctx, Node{ID: 1, Name: "v.mp4"}); err != nil {
		t.Fatal(err)
	}
	n, _, _ = i.NodeByID(ctx, 1)
	if len(n.Thumb) != 0 {
		t.Fatalf("不带 Thumb 的覆盖写应清空列，实际 %d bytes", len(n.Thumb))
	}
}

// TestEnsureNodesMigratesLegacySchema 旧版表结构（无 thumb 列）打开时自动补列。
func TestEnsureNodesMigratesLegacySchema(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "legacy.db")
	// 先手工造一个旧版表结构
	legacy, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = legacy.ExecContext(t.Context(), `
CREATE TABLE IF NOT EXISTS nodes (
	id INTEGER PRIMARY KEY,
	parent_id INTEGER NOT NULL,
	name TEXT NOT NULL,
	is_dir INTEGER NOT NULL,
	path TEXT NOT NULL,
	document_id INTEGER NOT NULL DEFAULT 0,
	access_hash INTEGER NOT NULL DEFAULT 0,
	file_reference BLOB,
	size INTEGER NOT NULL DEFAULT 0,
	mtime INTEGER NOT NULL DEFAULT 0,
	sha256 TEXT NOT NULL DEFAULT ''
)`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.ExecContext(t.Context(), `INSERT INTO nodes(id, parent_id, name, is_dir, path, sha256) VALUES(1, 0, 'old.txt', 0, '/old.txt', 'x')`); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}

	i, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open 应自动迁移: %v", err)
	}
	t.Cleanup(func() { _ = i.Close() })

	// 旧数据仍在
	n, ok, err := i.NodeByID(t.Context(), 1)
	if err != nil || !ok || n.Name != "old.txt" {
		t.Fatalf("迁移后旧数据丢失: ok=%v n=%+v err=%v", ok, n, err)
	}
	// 新列可写
	thumb := []byte{0x01, 0x02}
	if err := i.PutNode(t.Context(), Node{ID: 2, Name: "v.mp4", Thumb: thumb}); err != nil {
		t.Fatal(err)
	}
	got, _, err := i.NodeByID(t.Context(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Thumb) != string(thumb) {
		t.Fatalf("迁移后 thumb 列不可写: %d bytes", len(got.Thumb))
	}
}
