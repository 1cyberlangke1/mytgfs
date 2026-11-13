package index

// 本地 SQLite 物化索引。
//
// 索引是可丢弃缓存：删除后可由 Telegram 消息重放重建。
// 同 path 只保留 message_id 最大的版本（后写赢）。

import (
	"context"
	"database/sql"
	"fmt"
	"path"
	"sort"
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
	// Sha256 是文件内容的摘要（小写十六进制），用于同目录去重。
	// 空字符串表示未知（旧版本写入的记录、未回填的历史消息），不参与去重比对。
	Sha256 string
}

// Entry 是目录列表里的一项。
type Entry struct {
	Name  string
	IsDir bool
	File  *File
}

// SearchHit 是递归搜索命中的一条记录。
//
// 与 Entry 的区别：Path 是卷内绝对路径，搜索结果跨目录时必须带上。
type SearchHit struct {
	Path  string
	IsDir bool
	Size  int64
	Mtime int64
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
	mtime INTEGER NOT NULL,
	sha256 TEXT NOT NULL DEFAULT ''
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
CREATE TABLE IF NOT EXISTS pending (
	path TEXT PRIMARY KEY,
	temp TEXT NOT NULL,
	size INTEGER NOT NULL,
	mtime INTEGER NOT NULL
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

// PendingUpload 是一条已写入临时文件、但还没成功上传的记录。
//
// 崩溃或断网时提交中断，重启后按这张表把内容补传上去，
// 避免用户刚写的内容只存在于临时目录里。
type PendingUpload struct {
	Path  string
	Temp  string
	Size  int64
	Mtime int64
}

// PutPending 记录一条待补传的写入。
//
// 输入：p 待上传记录（Path 为主键）
// 输出：error
// 预期：同一路径重复记录时覆盖为最新的临时文件
func (i *Index) PutPending(ctx context.Context, p PendingUpload) error {
	_, err := i.db.ExecContext(ctx, `
INSERT INTO pending(path, temp, size, mtime) VALUES(?, ?, ?, ?)
ON CONFLICT(path) DO UPDATE SET
	temp = excluded.temp,
	size = excluded.size,
	mtime = excluded.mtime`,
		p.Path, p.Temp, p.Size, p.Mtime)
	return err
}

// DeletePending 删除一条待补传记录。
//
// 输入：path
// 输出：error
// 预期：幂等，记录不存在不报错
func (i *Index) DeletePending(ctx context.Context, path string) error {
	_, err := i.db.ExecContext(ctx, `DELETE FROM pending WHERE path = ?`, path)
	return err
}

// ListPending 列出全部待补传记录。
//
// 输入：无
// 输出：按 mtime 升序的记录
// 预期：无记录时返回空切片
func (i *Index) ListPending(ctx context.Context) ([]PendingUpload, error) {
	rows, err := i.db.QueryContext(ctx, `SELECT path, temp, size, mtime FROM pending ORDER BY mtime`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PendingUpload
	for rows.Next() {
		var p PendingUpload
		if err := rows.Scan(&p.Path, &p.Temp, &p.Size, &p.Mtime); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
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
INSERT INTO files(path, message_id, document_id, access_hash, file_reference, size, mtime, sha256)
VALUES(?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(path) DO UPDATE SET
	message_id = excluded.message_id,
	document_id = excluded.document_id,
	access_hash = excluded.access_hash,
	file_reference = excluded.file_reference,
	size = excluded.size,
	mtime = excluded.mtime,
	sha256 = excluded.sha256
WHERE excluded.message_id > files.message_id`,
		f.Path, f.MessageID, f.DocumentID, f.AccessHash, f.FileReference, f.Size, f.Mtime, f.Sha256)
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
INSERT INTO files(path, message_id, document_id, access_hash, file_reference, size, mtime, sha256)
VALUES(?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(path) DO UPDATE SET
	message_id = excluded.message_id,
	document_id = excluded.document_id,
	access_hash = excluded.access_hash,
	file_reference = excluded.file_reference,
	size = excluded.size,
	mtime = excluded.mtime,
	sha256 = excluded.sha256`,
		f.Path, f.MessageID, f.DocumentID, f.AccessHash, f.FileReference, f.Size, f.Mtime, f.Sha256)
	return err
}

// Get 按路径查询。
func (i *Index) Get(ctx context.Context, path string) (File, bool, error) {
	row := i.db.QueryRowContext(ctx, `
SELECT path, message_id, document_id, access_hash, file_reference, size, mtime, sha256
FROM files WHERE path = ?`, path)
	var f File
	err := row.Scan(&f.Path, &f.MessageID, &f.DocumentID, &f.AccessHash, &f.FileReference, &f.Size, &f.Mtime, &f.Sha256)
	if err == sql.ErrNoRows {
		return File{}, false, nil
	}
	if err != nil {
		return File{}, false, err
	}
	return f, true, nil
}

// FindContent 在整块盘（整个频道）里查找内容相同的文件，用于引用复用。
//
// 输入：dir 写入方所在目录（同目录优先）；size 字节数；sha 内容摘要
// 输出：命中的记录、是否存在、error
// 预期：sha 为空时判定未命中（旧记录没有摘要，不参与复用）；
//
//	先同目录，再按消息号从新到旧，保证复用的 document 尽量新、引用尽量有效
func (i *Index) FindContent(ctx context.Context, dir string, size int64, sha string) (File, bool, error) {
	if sha == "" {
		return File{}, false, nil
	}
	prefix := normalizeDir(dir)
	row := i.db.QueryRowContext(ctx, `
SELECT path, message_id, document_id, access_hash, file_reference, size, mtime, sha256
FROM files
WHERE size = ? AND sha256 = ?
ORDER BY CASE WHEN path LIKE ? THEN 0 ELSE 1 END, message_id DESC
LIMIT 1`, size, sha, prefix+"%")
	var f File
	err := row.Scan(&f.Path, &f.MessageID, &f.DocumentID, &f.AccessHash, &f.FileReference, &f.Size, &f.Mtime, &f.Sha256)
	if err == sql.ErrNoRows {
		return File{}, false, nil
	}
	if err != nil {
		return File{}, false, err
	}
	return f, true, nil
}

// CountRefs 统计某个 document 还有多少条路径在引用。
//
// 输入：documentID Telegram document ID
// 输出：引用条数、error
// 预期：每条文件消息算一个引用；归零表示这份内容已经没有任何路径在用
func (i *Index) CountRefs(ctx context.Context, documentID int64) (int, error) {
	if documentID == 0 {
		return 0, nil
	}
	var n int
	err := i.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM files WHERE document_id = ?`, documentID).Scan(&n)
	return n, err
}

// List 列出目录的直接子项。
//
// 输入：dir 目录路径（"/" 表示根）
// 输出：直接子文件与子目录（含显式创建的空目录）
// 预期：目录可由子项推导，也可由 dirs 表显式记录
func (i *Index) List(ctx context.Context, dir string) ([]Entry, error) {
	prefix := normalizeDir(dir)
	rows, err := i.db.QueryContext(ctx, `
SELECT path, message_id, document_id, access_hash, file_reference, size, mtime, sha256
FROM files WHERE path LIKE ?`, prefix+"%")
	if err != nil {
		return nil, err
	}

	seen := map[string]bool{}
	var out []Entry
	for rows.Next() {
		var f File
		if err := rows.Scan(&f.Path, &f.MessageID, &f.DocumentID, &f.AccessHash, &f.FileReference, &f.Size, &f.Mtime, &f.Sha256); err != nil {
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

// CountFiles 返回索引里的文件条数。
//
// 输入：无
// 输出：文件数；error
// 预期：用于判断索引是否需要从频道重建
func (i *Index) CountFiles(ctx context.Context) (int, error) {
	var n int
	err := i.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM files`).Scan(&n)
	return n, err
}

// Used 返回索引里所有文件大小合计，供 Statfs 显示已用空间。
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
SELECT path, message_id, document_id, access_hash, file_reference, size, mtime, sha256
FROM files WHERE path LIKE ?`, prefix+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []File
	for rows.Next() {
		var f File
		if err := rows.Scan(&f.Path, &f.MessageID, &f.DocumentID, &f.AccessHash, &f.FileReference, &f.Size, &f.Mtime, &f.Sha256); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// defaultSearchLimit 是搜索默认返回的最大命中数。
const defaultSearchLimit = 500

// Search 在 dir 子树里按名字子串查找文件与目录。
//
// 输入：dir 起点目录；query 大小写不敏感子串；limit 最大命中数（<=0 用默认值）
// 输出：命中项，目录在前、同类型按路径升序
// 预期：
//   - 只匹配路径最后一段，父目录名字不参与命中
//   - query 为空时返回空结果，不是全量
func (i *Index) Search(ctx context.Context, dir, query string, limit int) ([]SearchHit, error) {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = defaultSearchLimit
	}
	files, err := i.ListPrefix(ctx, dir)
	if err != nil {
		return nil, err
	}
	dirs, err := i.Dirs(ctx, dir)
	if err != nil {
		return nil, err
	}
	root := normalizeDir(dir)
	out := make([]SearchHit, 0, 16)
	for _, p := range dirs {
		if p == root || !strings.Contains(strings.ToLower(path.Base(p)), q) {
			continue
		}
		out = append(out, SearchHit{Path: p, IsDir: true})
	}
	for _, f := range files {
		if !strings.Contains(strings.ToLower(path.Base(f.Path)), q) {
			continue
		}
		out = append(out, SearchHit{Path: f.Path, Size: f.Size, Mtime: f.Mtime})
	}
	sort.Slice(out, func(a, b int) bool {
		if out[a].IsDir != out[b].IsDir {
			return out[a].IsDir
		}
		return out[a].Path < out[b].Path
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
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
