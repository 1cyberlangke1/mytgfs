package index

// 节点表：远端 dirent 模型的本地物化。
//
// 每个节点对应频道里的一条消息，id 就是消息号；目录的子项名字存在父目录消息里，
// 本地 path 只是派生缓存，改目录名时单条 SQL 前缀替换即可。

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// nodesSchema 建表语句。
//
// entry_msg 记录「持有该节点 dirent 条目」的消息号：普通文件/目录是父目录头消息，
// 目录项溢出到续块时可能落在某条 chunk 上，用来定位改动发生后该刷新哪个目录。
const nodesSchema = `
CREATE TABLE IF NOT EXISTS nodes (
	id INTEGER PRIMARY KEY,
	parent_id INTEGER NOT NULL,
	name TEXT NOT NULL,
	is_dir INTEGER NOT NULL,
	path TEXT NOT NULL,
	entry_msg INTEGER NOT NULL DEFAULT 0,
	document_id INTEGER NOT NULL DEFAULT 0,
	access_hash INTEGER NOT NULL DEFAULT 0,
	file_reference BLOB,
	size INTEGER NOT NULL DEFAULT 0,
	mtime INTEGER NOT NULL DEFAULT 0,
	sha256 TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_nodes_name ON nodes(parent_id, name);
CREATE INDEX IF NOT EXISTS idx_nodes_path ON nodes(path);
CREATE TABLE IF NOT EXISTS dir_chunks (
	msg_id INTEGER PRIMARY KEY,
	dir_id INTEGER NOT NULL
);
`

// Node 是一个文件或目录节点。
type Node struct {
	// ID 是节点自己的消息号，根节点是置顶消息号。
	ID int
	// ParentID 是父目录消息号，根节点为 0。
	ParentID int
	Name     string
	IsDir    bool
	Path     string
	// EntryMsg 是持有本节点目录项的消息号（目录头消息或续块）。
	EntryMsg      int
	DocumentID    int64
	AccessHash    int64
	FileReference []byte
	Size          int64
	Mtime         int64
	Sha256        string
}

// ensureNodes 建节点表；Open 时调用。
func (i *Index) ensureNodes(ctx context.Context) error {
	_, err := i.db.ExecContext(ctx, nodesSchema)
	return err
}

// nodeColumns 是节点查询的固定列顺序。
const nodeColumns = `id, parent_id, name, is_dir, path, entry_msg, document_id, access_hash, file_reference, size, mtime, sha256`

// scanNode 把一行扫进 Node。
func scanNode(sc interface{ Scan(...any) error }) (Node, error) {
	var n Node
	var isDir int
	err := sc.Scan(&n.ID, &n.ParentID, &n.Name, &isDir, &n.Path, &n.EntryMsg,
		&n.DocumentID, &n.AccessHash, &n.FileReference, &n.Size, &n.Mtime, &n.Sha256)
	n.IsDir = isDir != 0
	return n, err
}

// SetRoot 记录根节点（置顶消息）。
//
// 输入：id 根消息号
// 输出：error
// 预期：已有根则改 id 并清空旧根行，path 固定为 "/"
func (i *Index) SetRoot(ctx context.Context, id int) error {
	if id <= 0 {
		return errors.New("index: root id must be positive")
	}
	if _, err := i.db.ExecContext(ctx, `DELETE FROM nodes WHERE parent_id = 0`); err != nil {
		return err
	}
	_, err := i.db.ExecContext(ctx, `
INSERT INTO nodes(id, parent_id, name, is_dir, path, mtime) VALUES(?, 0, '', 1, '/', ?)
ON CONFLICT(id) DO UPDATE SET parent_id=0, name='', is_dir=1, path='/'`, id, nowUnix())
	return err
}

// Root 返回根节点。
//
// 输入：无
// 输出：根节点、是否存在、error
// 预期：空卷返回 ok=false
func (i *Index) Root(ctx context.Context) (Node, bool, error) {
	row := i.db.QueryRowContext(ctx, `SELECT `+nodeColumns+` FROM nodes WHERE parent_id = 0 LIMIT 1`)
	n, err := scanNode(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Node{}, false, nil
	}
	if err != nil {
		return Node{}, false, err
	}
	return n, true, nil
}

// NodeByID 按节点消息号查询。
func (i *Index) NodeByID(ctx context.Context, id int) (Node, bool, error) {
	if id <= 0 {
		return Node{}, false, nil
	}
	row := i.db.QueryRowContext(ctx, `SELECT `+nodeColumns+` FROM nodes WHERE id = ?`, id)
	n, err := scanNode(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Node{}, false, nil
	}
	if err != nil {
		return Node{}, false, err
	}
	return n, true, nil
}

// NodeByPath 按派生路径查询。
func (i *Index) NodeByPath(ctx context.Context, p string) (Node, bool, error) {
	row := i.db.QueryRowContext(ctx, `SELECT `+nodeColumns+` FROM nodes WHERE path = ?`, cleanPath(p))
	n, err := scanNode(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Node{}, false, nil
	}
	if err != nil {
		return Node{}, false, err
	}
	return n, true, nil
}

// Child 在父目录下按名字找直接子项。
func (i *Index) Child(ctx context.Context, parentID int, name string) (Node, bool, error) {
	row := i.db.QueryRowContext(ctx, `SELECT `+nodeColumns+` FROM nodes WHERE parent_id = ? AND name = ?`, parentID, name)
	n, err := scanNode(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Node{}, false, nil
	}
	if err != nil {
		return Node{}, false, err
	}
	return n, true, nil
}

// Children 列出父目录的直接子项，按目录优先、名字升序。
func (i *Index) Children(ctx context.Context, parentID int) ([]Node, error) {
	rows, err := i.db.QueryContext(ctx, `SELECT `+nodeColumns+`
FROM nodes WHERE parent_id = ? ORDER BY is_dir DESC, name`, parentID)
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

// PutNode 写入或覆盖一个节点。
//
// 输入：n 节点（path 为空时按父目录推导）
// 输出：error
// 预期：同 id 覆盖；同父同名冲突时覆盖旧行（消息号以传入为准）
func (i *Index) PutNode(ctx context.Context, n Node) error {
	p := n.Path
	if p == "" {
		var err error
		p, err = i.derivePath(ctx, n.ParentID, n.Name)
		if err != nil {
			return err
		}
	}
	isDir := 0
	if n.IsDir {
		isDir = 1
	}
	_, err := i.db.ExecContext(ctx, `
INSERT INTO nodes(id, parent_id, name, is_dir, path, entry_msg, document_id, access_hash, file_reference, size, mtime, sha256)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
	parent_id=excluded.parent_id, name=excluded.name, is_dir=excluded.is_dir, path=excluded.path,
	entry_msg=excluded.entry_msg, document_id=excluded.document_id, access_hash=excluded.access_hash,
	file_reference=excluded.file_reference, size=excluded.size, mtime=excluded.mtime, sha256=excluded.sha256`,
		n.ID, n.ParentID, n.Name, isDir, p, n.EntryMsg, n.DocumentID, n.AccessHash,
		n.FileReference, n.Size, n.Mtime, n.Sha256)
	return err
}

// derivePath 由父目录与名字推出完整路径。
//
// 输入：parentID 父节点消息号；name 节点名
// 输出："/a/b" 形式路径
// 预期：父节点不存在时报错，避免写出无根路径
func (i *Index) derivePath(ctx context.Context, parentID int, name string) (string, error) {
	if parentID == 0 {
		return "/", nil
	}
	parent, ok, err := i.NodeByID(ctx, parentID)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("index: parent node %d not found", parentID)
	}
	return joinPath(parent.Path, name), nil
}

// MoveNode 把节点改名或换父目录，并同步整棵子树的派生路径。
//
// 输入：id 节点消息号；newParent 新父目录消息号；newName 新名字
// 输出：旧路径、新路径、error
// 预期：
//   - O(1) 条 SQL：子树路径用前缀替换一次更新
//   - 兄弟目录已有同名节点时报错，调用方应先自行判定冲突
func (i *Index) MoveNode(ctx context.Context, id, newParent int, newName string) (string, string, error) {
	n, ok, err := i.NodeByID(ctx, id)
	if err != nil {
		return "", "", err
	}
	if !ok {
		return "", "", fmt.Errorf("index: node %d not found", id)
	}
	if newParent == id {
		return "", "", errors.New("index: cannot move node into itself")
	}
	existing, busy, err := i.Child(ctx, newParent, newName)
	if err != nil {
		return "", "", err
	}
	if busy && existing.ID != id {
		return "", "", fmt.Errorf("index: name %q already exists", newName)
	}
	newPath, err := i.derivePath(ctx, newParent, newName)
	if err != nil {
		return "", "", err
	}
	if n.IsDir {
		inside, err := i.isInside(ctx, n, newParent)
		if err != nil {
			return "", "", err
		}
		if inside {
			return "", "", errors.New("index: cannot move directory into its subtree")
		}
	}
	oldPath := n.Path
	err = i.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
UPDATE nodes SET
	parent_id = CASE WHEN id = ? THEN ? ELSE parent_id END,
	name = CASE WHEN id = ? THEN ? ELSE name END,
	path = ? || substr(path, ?)
WHERE path = ? OR substr(path, 1, ?) = ?`,
			id, newParent, id, newName,
			newPath, len(oldPath)+1,
			oldPath, len(oldPath)+1, oldPath+"/"); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return oldPath, oldPath, err
	}
	return oldPath, newPath, nil
}

// isInside 判断 target 是否是 n 自己或其后代。
func (i *Index) isInside(ctx context.Context, n Node, target int) (bool, error) {
	if target == n.ID {
		return true, nil
	}
	t, ok, err := i.NodeByID(ctx, target)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}
	if t.Path == n.Path {
		return true, nil
	}
	return strings.HasPrefix(t.Path, subtreePrefix(n.Path)), nil
}

// subtreePrefix 返回目录子树的路径前缀；根目录是 "/"，其它是 "/a/b/"。
func subtreePrefix(dir string) string {
	if dir == "/" || dir == "" {
		return "/"
	}
	return dir + "/"
}

// SubtreeIDs 返回节点自己与全部后代的节点消息号。
//
// 输入：id 根节点消息号
// 输出：节点号列表（含自己），深度优先
func (i *Index) SubtreeIDs(ctx context.Context, id int) ([]int, error) {
	rows, err := i.db.QueryContext(ctx, `
WITH RECURSIVE sub(id) AS (
	SELECT id FROM nodes WHERE id = ?
	UNION
	SELECT n.id FROM nodes n JOIN sub s ON n.parent_id = s.id
)
SELECT id FROM sub`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// DeleteSubtree 删除节点与其全部后代，返回被删的消息号供远端删除。
//
// 输入：id 节点消息号
// 输出：被删节点号；error
// 预期：同时清掉这些目录的续块登记
func (i *Index) DeleteSubtree(ctx context.Context, id int) ([]int, error) {
	ids, err := i.SubtreeIDs(ctx, id)
	if err != nil || len(ids) == 0 {
		return nil, err
	}
	if err := i.tx(ctx, func(tx *sql.Tx) error {
		for _, v := range ids {
			if _, err := tx.ExecContext(ctx, `DELETE FROM dir_chunks WHERE dir_id = ?`, v); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM nodes WHERE id = ?`, v); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return ids, nil
}

// DeleteNode 删除单个节点。
func (i *Index) DeleteNode(ctx context.Context, id int) error {
	if id <= 0 {
		return nil
	}
	if _, err := i.db.ExecContext(ctx, `DELETE FROM nodes WHERE id = ?`, id); err != nil {
		return err
	}
	_, err := i.db.ExecContext(ctx, `DELETE FROM dir_chunks WHERE msg_id = ?`, id)
	return err
}

// PutChunk 登记一条目录续块属于哪个目录。
func (i *Index) PutChunk(ctx context.Context, dirID, msgID int) error {
	_, err := i.db.ExecContext(ctx, `
INSERT INTO dir_chunks(msg_id, dir_id) VALUES(?, ?)
ON CONFLICT(msg_id) DO UPDATE SET dir_id=excluded.dir_id`, msgID, dirID)
	return err
}

// ChunkOwner 反查续块所属目录。
func (i *Index) ChunkOwner(ctx context.Context, msgID int) (int, bool, error) {
	var dir int
	err := i.db.QueryRowContext(ctx, `SELECT dir_id FROM dir_chunks WHERE msg_id = ?`, msgID).Scan(&dir)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return dir, true, nil
}

// DeleteChunks 删掉某目录除 keep 之外的全部续块登记，返回被删消息号。
//
// 输入：dirID 目录消息号；keep 要保留的续块消息号集合
// 输出：被删续块消息号
func (i *Index) DeleteChunks(ctx context.Context, dirID int, keep map[int]bool) ([]int, error) {
	rows, err := i.db.QueryContext(ctx, `SELECT msg_id FROM dir_chunks WHERE dir_id = ?`, dirID)
	if err != nil {
		return nil, err
	}
	var drop []int
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return nil, err
		}
		if !keep[v] {
			drop = append(drop, v)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, v := range drop {
		if _, err := i.db.ExecContext(ctx, `DELETE FROM dir_chunks WHERE msg_id = ?`, v); err != nil {
			return nil, err
		}
	}
	return drop, nil
}

// SetEntryMsg 更新节点「目录项所在消息」的记账。
func (i *Index) SetEntryMsg(ctx context.Context, id, entryMsg int) error {
	_, err := i.db.ExecContext(ctx, `UPDATE nodes SET entry_msg = ? WHERE id = ?`, entryMsg, id)
	return err
}

// FileNodes 返回目录子树里所有文件节点。
func (i *Index) FileNodes(ctx context.Context, dirPath string) ([]Node, error) {
	base := cleanPath(dirPath)
	var (
		rows *sql.Rows
		err  error
	)
	if base == "/" {
		rows, err = i.db.QueryContext(ctx, `SELECT `+nodeColumns+` FROM nodes WHERE is_dir = 0 ORDER BY path`)
	} else {
		rows, err = i.db.QueryContext(ctx, `SELECT `+nodeColumns+`
FROM nodes WHERE is_dir = 0 AND (path = ? OR substr(path, 1, ?) = ?) ORDER BY path`,
			base, len(base)+1, base+"/")
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

// tx 在一个事务里执行 fn。
func (i *Index) tx(ctx context.Context, fn func(*sql.Tx) error) error {
	t, err := i.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(t); err != nil {
		_ = t.Rollback()
		return err
	}
	return t.Commit()
}

// joinPath 拼父路径与名字。
func joinPath(parent, name string) string {
	if parent == "" || parent == "/" {
		return "/" + name
	}
	return parent + "/" + name
}

// cleanPath 规整成不带尾斜杠的绝对路径；根为 "/"。
func cleanPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" || p == "/" {
		return "/"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	for strings.HasSuffix(p, "/") && p != "/" {
		p = strings.TrimSuffix(p, "/")
	}
	if p == "" {
		return "/"
	}
	return p
}
