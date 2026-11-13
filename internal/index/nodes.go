package index

// 节点表：远端父指针模型的本地物化。
//
// 每个节点对应频道里的一条消息，id 就是消息号；名字与父消息号写在节点自己的
// 消息里，本地 path 只是派生缓存，改名/移动时用单条 SQL 前缀替换刷新子树。

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// nodesSchema 建表语句。
//
// parent_id = 0 表示卷根下的节点；卷根本身不是节点，不占消息。
const nodesSchema = `
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
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_nodes_name ON nodes(parent_id, name);
CREATE INDEX IF NOT EXISTS idx_nodes_path ON nodes(path);
`

// Node 是一个文件或目录节点。
type Node struct {
	// ID 是节点自己的消息号。
	ID int
	// ParentID 是父目录消息号，卷根下的节点为 0。
	ParentID int
	Name     string
	IsDir    bool
	// Path 是按父子关系推导出来的卷内绝对路径。
	Path          string
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
const nodeColumns = `id, parent_id, name, is_dir, path, document_id, access_hash, file_reference, size, mtime, sha256`

// scanNode 把一行扫进 Node。
func scanNode(sc interface{ Scan(...any) error }) (Node, error) {
	var n Node
	var isDir int
	err := sc.Scan(&n.ID, &n.ParentID, &n.Name, &isDir, &n.Path,
		&n.DocumentID, &n.AccessHash, &n.FileReference, &n.Size, &n.Mtime, &n.Sha256)
	n.IsDir = isDir != 0
	return n, err
}

// NodeByID 按节点消息号查询。
//
// 输入：ctx；id 消息号
// 输出：节点、是否存在、error
// 预期：id<=0 直接返回不存在，不查库
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
//
// 输入：ctx；parentID 父目录消息号（0 表示卷根）
// 输出：直接子项
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
// 输入：n 节点（Path 为空时按父目录与名字推导）
// 输出：error
// 预期：
//   - 同 id 覆盖；同 (parent_id, name) 上的旧行被顶掉，避免旧记录挡住新消息
//   - 父节点不存在时推导失败并报错，防止写出无根路径
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
INSERT OR REPLACE INTO nodes(id, parent_id, name, is_dir, path, document_id, access_hash, file_reference, size, mtime, sha256)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		n.ID, n.ParentID, n.Name, isDir, p, n.DocumentID, n.AccessHash,
		n.FileReference, n.Size, n.Mtime, n.Sha256)
	return err
}

// derivePath 由父目录与名字推出完整路径。
//
// 输入：parentID 父节点消息号（0 表示卷根）；name 节点名
// 输出："/a/b" 形式路径
// 预期：父节点不存在时报错，避免写出无根路径
func (i *Index) derivePath(ctx context.Context, parentID int, name string) (string, error) {
	if parentID == 0 {
		return "/" + name, nil
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
// 输入：id 节点消息号；newParent 新父目录消息号（0 表示卷根）；newName 新名字
// 输出：旧路径、新路径、error
// 预期：
//   - 子树路径用一次前缀替换更新，与文件数量无关
//   - 目录不能移进自己的子树；兄弟目录已有同名节点时报错
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
	if n.IsDir {
		inside, err := i.IsDescendant(ctx, id, newParent)
		if err != nil {
			return "", "", err
		}
		if inside {
			return "", "", errors.New("index: cannot move directory into its subtree")
		}
	}
	if existing, busy, err := i.Child(ctx, newParent, newName); err != nil {
		return "", "", err
	} else if busy && existing.ID != id {
		return "", "", fmt.Errorf("index: name %q already exists", newName)
	}
	newPath, err := i.derivePath(ctx, newParent, newName)
	if err != nil {
		return "", "", err
	}
	oldPath := n.Path
	err = i.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`UPDATE nodes SET parent_id = ?, name = ?, path = ? WHERE id = ?`,
			newParent, newName, newPath, id); err != nil {
			return err
		}
		if newPath == oldPath {
			return nil
		}
		_, err := tx.ExecContext(ctx,
			`UPDATE nodes SET path = ? || substr(path, ?) WHERE substr(path, 1, ?) = ?`,
			newPath, len(oldPath)+1, len(oldPath)+1, oldPath+"/")
		return err
	})
	if err != nil {
		return oldPath, oldPath, err
	}
	return oldPath, newPath, nil
}

// IsDescendant 判断 id 是否落在 ancestorID 的子树里（含自己）。
//
// 输入：ancestorID 目录消息号；id 待判定节点
// 输出：true 表示 id 是 ancestorID 自己或其后代；error
// 预期：沿 parent_id 上溯，带 visited 防成环；上溯到卷根即停止
func (i *Index) IsDescendant(ctx context.Context, ancestorID, id int) (bool, error) {
	if ancestorID <= 0 {
		return true, nil
	}
	cur := id
	seen := map[int]bool{}
	for cur > 0 {
		if cur == ancestorID {
			return true, nil
		}
		if seen[cur] {
			return false, nil
		}
		seen[cur] = true
		n, ok, err := i.NodeByID(ctx, cur)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
		cur = n.ParentID
	}
	return false, nil
}

// SubtreeIDs 返回节点自己与全部后代的节点消息号。
//
// 输入：id 根节点消息号
// 输出：节点号列表（含自己）
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
func (i *Index) DeleteSubtree(ctx context.Context, id int) ([]int, error) {
	ids, err := i.SubtreeIDs(ctx, id)
	if err != nil || len(ids) == 0 {
		return nil, err
	}
	if err := i.tx(ctx, func(tx *sql.Tx) error {
		for _, v := range ids {
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
	_, err := i.db.ExecContext(ctx, `DELETE FROM nodes WHERE id = ?`, id)
	return err
}

// AllNodes 返回节点表里的全部记录。
//
// 输入：ctx
// 输出：按路径升序的节点
// 预期：供对账与展示遍历，不访问远端
func (i *Index) AllNodes(ctx context.Context) ([]Node, error) {
	rows, err := i.db.QueryContext(ctx, `SELECT `+nodeColumns+` FROM nodes ORDER BY path`)
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

// ClearNodes 清空节点表。
//
// 输入：ctx
// 输出：error
// 预期：只删本地缓存；远端消息一条都不动
func (i *Index) ClearNodes(ctx context.Context) error {
	_, err := i.db.ExecContext(ctx, `DELETE FROM nodes`)
	return err
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
