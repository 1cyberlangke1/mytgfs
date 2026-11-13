package index

// 本地 SQLite 物化索引。
//
// 索引是可丢弃缓存：删除后可由 Telegram 消息重建。
// 远端目录信息在节点表里（见 nodes.go），这里放待补传账本、路径搜索与状态游标。

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"

	_ "modernc.org/sqlite"
)

// SearchHit 是递归搜索命中的一条记录。
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

// schema 是索引里与节点无关的表。
const schema = `
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
CREATE TABLE IF NOT EXISTS pending_done (
	path TEXT PRIMARY KEY,
	message_id INTEGER NOT NULL,
	document_id INTEGER NOT NULL,
	access_hash INTEGER NOT NULL,
	file_reference BLOB,
	size INTEGER NOT NULL,
	sha256 TEXT NOT NULL DEFAULT '',
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
		return nil, err
	}
	idx := &Index{db: db}
	if err := idx.ensureNodes(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return idx, nil
}

// OpenMemory 打开内存索引（测试用）。
func OpenMemory() (*Index, error) {
	return Open(":memory:")
}

// PendingUpload 是一条已写入临时文件、但还没成功上传的记录。
type PendingUpload struct {
	Path  string
	Temp  string
	Size  int64
	Mtime int64
}

// PutPending 记录一条待补传的写入。
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
func (i *Index) DeletePending(ctx context.Context, path string) error {
	_, err := i.db.ExecContext(ctx, `DELETE FROM pending WHERE path = ?`, path)
	return err
}

// ListPending 列出全部待补传记录，按 mtime 升序。
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

// PendingDone 是一条「已经上传成功、但还没写进节点表」的记录。
type PendingDone struct {
	Path          string
	MessageID     int
	DocumentID    int64
	AccessHash    int64
	FileReference []byte
	Size          int64
	Sha256        string
	Mtime         int64
}

// PutPendingDone 记录一条已上传待索引的提交。
func (i *Index) PutPendingDone(ctx context.Context, p PendingDone) error {
	_, err := i.db.ExecContext(ctx, `
INSERT INTO pending_done(path, message_id, document_id, access_hash, file_reference, size, sha256, mtime)
VALUES(?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(path) DO UPDATE SET
	message_id = excluded.message_id,
	document_id = excluded.document_id,
	access_hash = excluded.access_hash,
	file_reference = excluded.file_reference,
	size = excluded.size,
	sha256 = excluded.sha256,
	mtime = excluded.mtime`,
		p.Path, p.MessageID, p.DocumentID, p.AccessHash, p.FileReference, p.Size, p.Sha256, p.Mtime)
	return err
}

// GetPendingDone 读取某路径的已上传待索引记录。
func (i *Index) GetPendingDone(ctx context.Context, path string) (PendingDone, bool, error) {
	row := i.db.QueryRowContext(ctx, `
SELECT path, message_id, document_id, access_hash, file_reference, size, sha256, mtime
FROM pending_done WHERE path = ?`, path)
	var p PendingDone
	err := row.Scan(&p.Path, &p.MessageID, &p.DocumentID, &p.AccessHash, &p.FileReference, &p.Size, &p.Sha256, &p.Mtime)
	if errors.Is(err, sql.ErrNoRows) {
		return PendingDone{}, false, nil
	}
	if err != nil {
		return PendingDone{}, false, err
	}
	return p, true, nil
}

// DeletePendingDone 删除一条已上传待索引记录。
func (i *Index) DeletePendingDone(ctx context.Context, path string) error {
	_, err := i.db.ExecContext(ctx, `DELETE FROM pending_done WHERE path = ?`, path)
	return err
}

// FindContent 在整个卷里查找内容相同的文件，用于引用复用。
//
// 输入：dir 写入方所在目录（同目录优先）；size 字节数；sha 内容摘要
// 输出：命中的节点、是否存在、error
// 预期：sha 为空时判定未命中；先同目录，再按消息号从新到旧
func (i *Index) FindContent(ctx context.Context, dir string, size int64, sha string) (Node, bool, error) {
	if sha == "" {
		return Node{}, false, nil
	}
	base := cleanPath(dir)
	row := i.db.QueryRowContext(ctx, `
SELECT `+nodeColumns+`
FROM nodes
WHERE is_dir = 0 AND size = ? AND sha256 = ?
ORDER BY CASE WHEN path = ? OR substr(path, 1, ?) = ? THEN 0 ELSE 1 END, id DESC
LIMIT 1`, size, sha, base, len(base)+1, base+"/")
	n, err := scanNode(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Node{}, false, nil
	}
	if err != nil {
		return Node{}, false, err
	}
	return n, true, nil
}

// CountRefs 统计某个 document 还有多少条路径在引用。
func (i *Index) CountRefs(ctx context.Context, documentID int64) (int, error) {
	if documentID == 0 {
		return 0, nil
	}
	var n int
	err := i.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM nodes WHERE is_dir = 0 AND document_id = ?`, documentID).Scan(&n)
	return n, err
}

// CountFiles 返回索引里的文件条数。
func (i *Index) CountFiles(ctx context.Context) (int, error) {
	var n int
	err := i.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM nodes WHERE is_dir = 0`).Scan(&n)
	return n, err
}

// Used 返回索引里所有文件大小合计。
func (i *Index) Used(ctx context.Context) (int64, error) {
	var n int64
	err := i.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(size), 0) FROM nodes WHERE is_dir = 0`).Scan(&n)
	return n, err
}

// defaultSearchLimit 是搜索默认返回的最大命中数。
const defaultSearchLimit = 500

// Search 在 dir 子树里按名字子串查找文件与目录。
//
// 输入：dir 起点目录；query 大小写不敏感子串；limit 最大命中数（<=0 用默认值）
// 输出：命中项，目录在前、同类型按路径升序
// 预期：只匹配最后一段名字；query 为空返回空结果
func (i *Index) Search(ctx context.Context, dir, query string, limit int) ([]SearchHit, error) {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = defaultSearchLimit
	}
	nodes, err := i.ListPrefix(ctx, dir)
	if err != nil {
		return nil, err
	}
	out := make([]SearchHit, 0, 16)
	for _, n := range nodes {
		if !strings.Contains(strings.ToLower(n.Name), q) {
			continue
		}
		out = append(out, SearchHit{Path: n.Path, IsDir: n.IsDir, Size: n.Size, Mtime: n.Mtime})
	}
	slices.SortFunc(out, func(a, b SearchHit) int {
		if a.IsDir != b.IsDir {
			if a.IsDir {
				return -1
			}
			return 1
		}
		return cmp.Compare(a.Path, b.Path)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// ListPrefix 返回目录子树的全部节点（不含目录自己）。
//
// 输入：dir 目录路径
// 输出：文件与目录节点，按路径升序
// 预期：根目录 "/" 返回整棵树
func (i *Index) ListPrefix(ctx context.Context, dir string) ([]Node, error) {
	base := cleanPath(dir)
	var (
		rows *sql.Rows
		err  error
	)
	if base == "/" {
		rows, err = i.db.QueryContext(ctx, `SELECT `+nodeColumns+` FROM nodes ORDER BY path`)
	} else {
		rows, err = i.db.QueryContext(ctx, `SELECT `+nodeColumns+`
FROM nodes WHERE substr(path, 1, ?) = ? ORDER BY path`, len(base)+1, base+"/")
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Node
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// PathByMessageID 按消息号查路径。
//
// 输入：messageID 频道消息 ID
// 输出：路径、是否存在、error
func (i *Index) PathByMessageID(ctx context.Context, messageID int) (string, bool, error) {
	if messageID <= 0 {
		return "", false, nil
	}
	var p string
	err := i.db.QueryRowContext(ctx, `SELECT path FROM nodes WHERE id = ? LIMIT 1`, messageID).Scan(&p)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return p, true, nil
}

// DeleteByMessageID 按消息号删除节点。
//
// 输入：messageID 频道消息 ID
// 输出：error
// 预期：不存在时不算错误
func (i *Index) DeleteByMessageID(ctx context.Context, messageID int) error {
	if messageID <= 0 {
		return nil
	}
	_, err := i.db.ExecContext(ctx, `DELETE FROM nodes WHERE id = ?`, messageID)
	return err
}

// GetMeta 读取一条持久化的整数状态。
func (i *Index) GetMeta(ctx context.Context, key string) (int64, bool, error) {
	var v int64
	err := i.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return v, true, nil
}

// PutMeta 写入一条持久化的整数状态。
func (i *Index) PutMeta(ctx context.Context, key string, value int64) error {
	_, err := i.db.ExecContext(ctx, `
INSERT INTO meta(key, value) VALUES(?, ?)
ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}
