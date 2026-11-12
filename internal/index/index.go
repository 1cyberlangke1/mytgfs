package index

// 本地 SQLite 物化索引。
//
// 索引是可丢弃缓存：删除后可由 Telegram 消息重放重建。
// 同 path 只保留 message_id 最大的版本（后写赢）。

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	_ "modernc.org/sqlite"
)

// File 是索引里的一条文件记录。
type File struct {
	Path          string
	MessageID     int
	DocumentID    int64
	AccessHash    int64
	FileReference []byte
	Size          int64
	Mtime         int64
}

// Entry 是目录列表里的一项。
type Entry struct {
	Name  string
	IsDir bool
	File  *File
}

// Index 是本地 SQLite 物化索引。
type Index struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS files (
	path TEXT PRIMARY KEY,
	message_id INTEGER NOT NULL,
	document_id INTEGER NOT NULL,
	access_hash INTEGER NOT NULL,
	file_reference BLOB,
	size INTEGER NOT NULL,
	mtime INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_files_message ON files(message_id);
`

// Open 打开或创建索引文件。
func Open(path string) (*Index, error) {
	dsn := path
	if path != ":memory:" {
		dsn = path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if path == ":memory:" {
		// 内存库每个连接独立，限制单连接保证同一个库
		db.SetMaxOpenConns(1)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}
	return &Index{db: db}, nil
}

// OpenMemory 打开内存索引（测试用）。
func OpenMemory() (*Index, error) {
	return Open(":memory:")
}

// Close 关闭索引。
func (i *Index) Close() error { return i.db.Close() }

// Put 写入或更新一条记录。
//
// 输入：f 文件记录
// 输出：error
// 预期：同 path 只保留 message_id 更大的版本（后写赢）
func (i *Index) Put(ctx context.Context, f File) error {
	_, err := i.db.ExecContext(ctx, `
INSERT INTO files(path, message_id, document_id, access_hash, file_reference, size, mtime)
VALUES(?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(path) DO UPDATE SET
	message_id = excluded.message_id,
	document_id = excluded.document_id,
	access_hash = excluded.access_hash,
	file_reference = excluded.file_reference,
	size = excluded.size,
	mtime = excluded.mtime
WHERE excluded.message_id > files.message_id`,
		f.Path, f.MessageID, f.DocumentID, f.AccessHash, f.FileReference, f.Size, f.Mtime)
	return err
}

// Get 按路径查询。
func (i *Index) Get(ctx context.Context, path string) (File, bool, error) {
	row := i.db.QueryRowContext(ctx, `
SELECT path, message_id, document_id, access_hash, file_reference, size, mtime
FROM files WHERE path = ?`, path)
	var f File
	err := row.Scan(&f.Path, &f.MessageID, &f.DocumentID, &f.AccessHash, &f.FileReference, &f.Size, &f.Mtime)
	if err == sql.ErrNoRows {
		return File{}, false, nil
	}
	if err != nil {
		return File{}, false, err
	}
	return f, true, nil
}

// List 列出目录的直接子项。
//
// 输入：dir 目录路径（"/" 表示根）
// 输出：直接子文件与子目录
// 预期：目录由 path 推导，不单独存储
func (i *Index) List(ctx context.Context, dir string) ([]Entry, error) {
	prefix := normalizeDir(dir)
	rows, err := i.db.QueryContext(ctx, `
SELECT path, message_id, document_id, access_hash, file_reference, size, mtime
FROM files WHERE path LIKE ?`, prefix+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	seen := map[string]bool{}
	var out []Entry
	for rows.Next() {
		var f File
		if err := rows.Scan(&f.Path, &f.MessageID, &f.DocumentID, &f.AccessHash, &f.FileReference, &f.Size, &f.Mtime); err != nil {
			return nil, err
		}
		rest := strings.TrimPrefix(f.Path, prefix)
		if rest == "" {
			continue
		}
		if idx := strings.IndexByte(rest, '/'); idx >= 0 {
			name := rest[:idx]
			if seen[name] {
				continue
			}
			seen[name] = true
			out = append(out, Entry{Name: name, IsDir: true})
			continue
		}
		ff := f
		out = append(out, Entry{Name: rest, File: &ff})
	}
	return out, rows.Err()
}

// Used 返回索引里所有文件的字节总数。
//
// 输入：ctx
// 输出：已用字节数、error
// 预期：空表返回 0
func (i *Index) Used(ctx context.Context) (int64, error) {
	var n int64
	err := i.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(size), 0) FROM files`).Scan(&n)
	return n, err
}

// Delete 删除一条记录。
func (i *Index) Delete(ctx context.Context, path string) error {
	_, err := i.db.ExecContext(ctx, `DELETE FROM files WHERE path = ?`, path)
	return err
}

// normalizeDir 把目录规整成 "/" 或 "/a/b/"。
func normalizeDir(dir string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" || dir == "/" {
		return "/"
	}
	if !strings.HasPrefix(dir, "/") {
		dir = "/" + dir
	}
	if !strings.HasSuffix(dir, "/") {
		dir += "/"
	}
	return dir
}
