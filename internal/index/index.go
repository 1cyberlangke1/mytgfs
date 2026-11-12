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
	"time"

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

// MetaKeyChannelPts 是频道 pts 游标在 meta 表里的键名。
const MetaKeyChannelPts = "channel_pts"

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
CREATE TABLE IF NOT EXISTS dirs (
	path TEXT PRIMARY KEY,
	mtime INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS meta (
	key TEXT PRIMARY KEY,
	value INTEGER NOT NULL
);
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

// Replace 无条件覆盖一条记录。
//
// 输入：f 文件记录
// 输出：error
// 预期：用于同一条消息的元数据刷新（file_reference 过期后重新拉取），
//
//	不受「message_id 更大才覆盖」的限制
func (i *Index) Replace(ctx context.Context, f File) error {
	_, err := i.db.ExecContext(ctx, `
INSERT INTO files(path, message_id, document_id, access_hash, file_reference, size, mtime)
VALUES(?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(path) DO UPDATE SET
	message_id = excluded.message_id,
	document_id = excluded.document_id,
	access_hash = excluded.access_hash,
	file_reference = excluded.file_reference,
	size = excluded.size,
	mtime = excluded.mtime`,
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
// 输出：直接子文件与子目录（含显式创建的空目录）
// 预期：目录可由子项推导，也可由 dirs 表显式记录
func (i *Index) List(ctx context.Context, dir string) ([]Entry, error) {
	prefix := normalizeDir(dir)
	rows, err := i.db.QueryContext(ctx, `
SELECT path, message_id, document_id, access_hash, file_reference, size, mtime
FROM files WHERE path LIKE ?`, prefix+"%")
	if err != nil {
		return nil, err
	}

	seen := map[string]bool{}
	var out []Entry
	for rows.Next() {
		var f File
		if err := rows.Scan(&f.Path, &f.MessageID, &f.DocumentID, &f.AccessHash, &f.FileReference, &f.Size, &f.Mtime); err != nil {
			rows.Close()
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
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	// 显式目录：空文件夹也能列出来
	dirRows, err := i.db.QueryContext(ctx, `SELECT path FROM dirs WHERE path LIKE ?`, prefix+"%")
	if err != nil {
		return nil, err
	}
	defer dirRows.Close()
	for dirRows.Next() {
		var p string
		if err := dirRows.Scan(&p); err != nil {
			return nil, err
		}
		rest := strings.TrimPrefix(p, prefix)
		if rest == "" {
			continue
		}
		if idx := strings.IndexByte(rest, '/'); idx >= 0 {
			rest = rest[:idx]
		}
		if seen[rest] {
			continue
		}
		seen[rest] = true
		out = append(out, Entry{Name: rest, IsDir: true})
	}
	return out, dirRows.Err()
}

// Used 返回索引里所有文件的字节总数。
func (i *Index) Used(ctx context.Context) (int64, error) {
	var n int64
	err := i.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(size), 0) FROM files`).Scan(&n)
	return n, err
}

// MaxMessageID 返回索引里最大的文件消息 ID，用于增量拉取。
func (i *Index) MaxMessageID(ctx context.Context) (int, error) {
	var n int
	err := i.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(message_id), 0) FROM files WHERE message_id > 0`).Scan(&n)
	return n, err
}

// PutDir 记录一个显式目录。
func (i *Index) PutDir(ctx context.Context, p string) error {
	_, err := i.db.ExecContext(ctx, `
INSERT INTO dirs(path, mtime) VALUES(?, ?)
ON CONFLICT(path) DO UPDATE SET mtime=excluded.mtime`, p, time.Now().Unix())
	return err
}

// DeleteDir 删除一条目录记录。
func (i *Index) DeleteDir(ctx context.Context, p string) error {
	_, err := i.db.ExecContext(ctx, `DELETE FROM dirs WHERE path = ?`, p)
	return err
}

// IsDir 判断路径是否为目录（显式目录或存在子项）。
func (i *Index) IsDir(ctx context.Context, p string) (bool, error) {
	if p == "/" {
		return true, nil
	}
	prefix := normalizeDir(p)
	var n int
	row := i.db.QueryRowContext(ctx, `
SELECT (SELECT COUNT(*) FROM files WHERE path LIKE ?)
     + (SELECT COUNT(*) FROM dirs WHERE path LIKE ?)`, prefix+"%", prefix+"%")
	if err := row.Scan(&n); err != nil {
		return false, err
	}
	if n > 0 {
		return true, nil
	}
	err := i.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM dirs WHERE path = ?`, p).Scan(&n)
	return n > 0, err
}

// ListPrefix 返回目录下所有文件（含深层）。
func (i *Index) ListPrefix(ctx context.Context, dir string) ([]File, error) {
	prefix := normalizeDir(dir)
	rows, err := i.db.QueryContext(ctx, `
SELECT path, message_id, document_id, access_hash, file_reference, size, mtime
FROM files WHERE path LIKE ?`, prefix+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []File
	for rows.Next() {
		var f File
		if err := rows.Scan(&f.Path, &f.MessageID, &f.DocumentID, &f.AccessHash, &f.FileReference, &f.Size, &f.Mtime); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// Dirs 返回目录下所有显式目录（含深层）。
func (i *Index) Dirs(ctx context.Context, dir string) ([]string, error) {
	prefix := normalizeDir(dir)
	rows, err := i.db.QueryContext(ctx, `SELECT path FROM dirs WHERE path = ? OR path LIKE ?`, dir, prefix+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Delete 删除一条记录。
func (i *Index) Delete(ctx context.Context, path string) error {
	_, err := i.db.ExecContext(ctx, `DELETE FROM files WHERE path = ?`, path)
	return err
}

// DeleteByMessageID 按 Telegram 消息号删除记录。
//
// 输入：messageID 频道消息 ID
// 输出：error
// 预期：频道侧删除/改名的推送到来时用它清掉旧行；不存在时不算错误
func (i *Index) DeleteByMessageID(ctx context.Context, messageID int) error {
	if messageID <= 0 {
		return nil
	}
	_, err := i.db.ExecContext(ctx, `DELETE FROM files WHERE message_id = ?`, messageID)
	return err
}

// PathByMessageID 按 Telegram 消息号查路径。
//
// 输入：messageID 频道消息 ID
// 输出：路径、是否存在、error
// 预期：供增量同步在删除消息前取出路径，好通知挂载点刷新；不存在时 ok=false
func (i *Index) PathByMessageID(ctx context.Context, messageID int) (string, bool, error) {
	if messageID <= 0 {
		return "", false, nil
	}
	var p string
	err := i.db.QueryRowContext(ctx, `SELECT path FROM files WHERE message_id = ? LIMIT 1`, messageID).Scan(&p)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return p, true, nil
}

// GetMeta 读取一条持久化的整数状态。
//
// 输入：key 状态名（如 channel_pts）
// 输出：值、是否存在、error
// 预期：不存在时返回 (0, false, nil)，不当作错误
func (i *Index) GetMeta(ctx context.Context, key string) (int64, bool, error) {
	var v int64
	err := i.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = ?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return v, true, nil
}

// PutMeta 写入一条持久化的整数状态。
//
// 输入：key 状态名、value 值
// 输出：error
// 预期：同 key 覆盖写；用于保存 pts 游标
func (i *Index) PutMeta(ctx context.Context, key string, value int64) error {
	_, err := i.db.ExecContext(ctx, `
INSERT INTO meta(key, value) VALUES(?, ?)
ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
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
