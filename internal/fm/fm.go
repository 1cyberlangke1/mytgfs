package fm

// 面板文件管理门面：在 vfs.FS 之上补齐路径规范、递归搜索、串行化变更。
//
// 删除与移动由底层按节点子树一次完成，这里不重复实现存储逻辑。
// 所有路径都是卷内绝对路径（以 / 开头），越界（含 ..）一律拒绝。

import (
	"cmp"
	"context"
	"errors"
	"io"
	"os"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"mytgfs/internal/vfs"

	"mytgfs/internal/i18n"
	"mytgfs/internal/index"
)

var (
	// ErrBadPath 表示路径越界或指向卷根，不允许操作。
	ErrBadPath = errors.New(i18n.T("err.fm_bad_path"))
	// ErrExists 表示目标已存在，不静默覆盖。
	ErrExists = errors.New(i18n.T("err.fm_exists"))
)

// Entry 是一条目录项。
type Entry struct {
	Name string    `json:"name"`
	Dir  bool      `json:"dir"`
	Size int64     `json:"size"`
	Mod  time.Time `json:"mod"`
}

// Files 是面板需要的文件管理能力。
type Files interface {
	List(ctx context.Context, dir string) ([]Entry, error)
	// Search 递归按名字查找；limit<=0 用默认上限
	Search(ctx context.Context, dir, query string, limit int) ([]SearchHit, error)
	Info(ctx context.Context, p string) (Entry, error)
	Mkdir(ctx context.Context, p string) error
	Upload(ctx context.Context, p string, r io.Reader) error
	Remove(ctx context.Context, p string) error
	Move(ctx context.Context, from, to string) error
	Open(ctx context.Context, p string) (vfs.File, error)
}

// Manager 是 Files 的实现。
type Manager struct {
	fs vfs.FS
	// mu 串行化变更操作：面板上连点两下不会互相踩改名/删除
	mu sync.Mutex
}

// New 创建文件管理门面。
//
// 输入：fsys 底层文件系统（通常是 vfs.FileSystem）
// 输出：*Manager
// 预期：只做路径规范化与组合，不重复实现存储逻辑
func New(fsys vfs.FS) *Manager { return &Manager{fs: fsys} }

// List 列出一个目录。
//
// 输入：dir 目录路径
// 输出：目录项，目录在前、同类型按名字不区分大小写排序
// 预期：目录不存在时返回底层错误（os.ErrNotExist）
func (m *Manager) List(ctx context.Context, dir string) ([]Entry, error) {
	d, err := clean(dir)
	if err != nil {
		return nil, err
	}
	f, err := m.fs.OpenFile(ctx, d, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	infos, err := f.Readdir(-1)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(infos))
	for _, fi := range infos {
		out = append(out, Entry{Name: fi.Name(), Dir: fi.IsDir(), Size: fi.Size(), Mod: fi.ModTime()})
	}
	slices.SortFunc(out, func(a, b Entry) int {
		if a.Dir != b.Dir {
			if a.Dir {
				return -1
			}
			return 1
		}
		return cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return out, nil
}

// SearchHit 是递归搜索命中的一条。
//
// 与 Entry 的区别：Path 是卷内绝对路径，命中项可能不在当前目录。
type SearchHit struct {
	Path string    `json:"path"`
	Dir  bool      `json:"dir"`
	Size int64     `json:"size"`
	Mod  time.Time `json:"mod"`
}

// Searcher 是底层文件系统的可选能力：索引级递归搜索。
//
// vfs.FileSystem 实现了它；没有实现的实现会退化成当前目录过滤。
type Searcher interface {
	Search(ctx context.Context, dir, query string, limit int) ([]index.SearchHit, error)
}

// Search 递归搜索名字包含 query 的文件与目录。
//
// 输入：dir 起点目录；query 大小写不敏感子串；limit 命中上限（<=0 用默认）
// 输出：命中项
// 预期：query 为空返回空结果；底层不支持递归搜索时只过滤当前目录
func (m *Manager) Search(ctx context.Context, dir, query string, limit int) ([]SearchHit, error) {
	c, err := clean(dir)
	if err != nil {
		return nil, err
	}
	needle := strings.TrimSpace(query)
	if needle == "" {
		return nil, nil
	}
	if s, ok := m.fs.(Searcher); ok {
		hits, herr := s.Search(ctx, c, needle, limit)
		if herr != nil {
			return nil, herr
		}
		out := make([]SearchHit, 0, len(hits))
		for _, h := range hits {
			out = append(out, SearchHit{Path: h.Path, Dir: h.IsDir, Size: h.Size, Mod: time.Unix(h.Mtime, 0)})
		}
		return out, nil
	}
	entries, lerr := m.List(ctx, c)
	if lerr != nil {
		return nil, lerr
	}
	lower := strings.ToLower(needle)
	var out []SearchHit
	for _, e := range entries {
		if !strings.Contains(strings.ToLower(e.Name), lower) {
			continue
		}
		out = append(out, SearchHit{Path: path.Join(c, e.Name), Dir: e.Dir, Size: e.Size, Mod: e.Mod})
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// Info 返回单个条目的信息。
func (m *Manager) Info(ctx context.Context, p string) (Entry, error) {
	c, err := clean(p)
	if err != nil {
		return Entry{}, err
	}
	fi, err := m.fs.Stat(ctx, c)
	if err != nil {
		return Entry{}, err
	}
	return Entry{Name: fi.Name(), Dir: fi.IsDir(), Size: fi.Size(), Mod: fi.ModTime()}, nil
}

// Mkdir 创建目录。
//
// 输入：p 新目录路径
// 输出：error；已存在返回 ErrExists，卷根返回 ErrBadPath
// 预期：父目录不必预先存在（索引按路径前缀推导）
func (m *Manager) Mkdir(ctx context.Context, p string) error {
	c, err := clean(p)
	if err != nil {
		return err
	}
	if c == "/" {
		return ErrBadPath
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, serr := m.fs.Stat(ctx, c); serr == nil {
		return ErrExists
	}
	return m.fs.Mkdir(ctx, c, 0o755)
}

// Upload 写入一个文件（覆盖已存在的同名文件）。
//
// 输入：p 目标路径；r 内容
// 输出：error
// 预期：
//   - 复制中途出错时丢弃临时文件，绝不提交半份内容
//   - 覆盖写由底层负责删掉旧消息
func (m *Manager) Upload(ctx context.Context, p string, r io.Reader) error {
	c, err := clean(p)
	if err != nil {
		return err
	}
	if c == "/" {
		return ErrBadPath
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	f, err := m.fs.OpenFile(ctx, c, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, cerr := io.Copy(f, r); cerr != nil {
		// 半份内容不能提交：底层实现支持 Abort 就丢弃临时文件
		if a, ok := f.(interface{ Abort() error }); ok {
			_ = a.Abort()
		} else {
			_ = f.Close()
		}
		return cerr
	}
	return f.Close()
}

// Remove 删除文件或目录。
//
// 输入：p 路径
// 输出：error
// 预期：目录整棵子树一次删掉（远端批量删消息），卷根拒绝
func (m *Manager) Remove(ctx context.Context, p string) error {
	c, err := clean(p)
	if err != nil {
		return err
	}
	if c == "/" {
		return ErrBadPath
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.fs.RemoveAll(ctx, c)
}

// Move 移动/改名，文件和目录都支持。
//
// 输入：from 源；to 目标（包含新名字的完整路径）
// 输出：error；目标已存在返回 ErrExists
// 预期：只编辑节点自己那条消息，不重传内容
func (m *Manager) Move(ctx context.Context, from, to string) error {
	src, err := clean(from)
	if err != nil {
		return err
	}
	dst, err := clean(to)
	if err != nil {
		return err
	}
	if src == "/" || dst == "/" {
		return ErrBadPath
	}
	if src == dst {
		return nil
	}
	if strings.HasPrefix(dst+"/", src+"/") {
		return ErrBadPath
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, serr := m.fs.Stat(ctx, dst); serr == nil {
		return ErrExists
	}
	return m.fs.Rename(ctx, src, dst)
}

// Open 以只读方式打开文件，供下载流式读取。
func (m *Manager) Open(ctx context.Context, p string) (vfs.File, error) {
	c, err := clean(p)
	if err != nil {
		return nil, err
	}
	return m.fs.OpenFile(ctx, c, os.O_RDONLY, 0)
}

// clean 规范化卷内路径。
//
// 输入：任意用户输入路径
// 输出：以 / 开头的干净路径
// 预期：出现 .. 段直接拒绝，防止越出卷根
func clean(p string) (string, error) {
	p = strings.ReplaceAll(p, `\`, "/")
	p = cmp.Or(p, "/")
	for seg := range strings.SplitSeq(p, "/") {
		if seg == ".." {
			return "", ErrBadPath
		}
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return path.Clean(p), nil
}
