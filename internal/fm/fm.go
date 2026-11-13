package fm

// 面板文件管理门面：在 webdav.FileSystem 之上补齐列出、递归删除、跨目录移动。
//
// 为什么单独一层：webdav.FileSystem 是按 WebDAV 语义设计的
//   - RemoveAll 对目录只删一条目录记录，不会删内容
//   - Rename 只支持文件，拒绝目录
// 面板需要「删目录连内容一起删」「文件/目录移到任意目录」，所以在这里补。
//
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

	"golang.org/x/net/webdav"

	"mytgfs/internal/index"
)

var (
	// ErrBadPath 表示路径越界或指向卷根，不允许操作。
	ErrBadPath = errors.New("invalid path")
	// ErrExists 表示目标已存在，不静默覆盖。
	ErrExists = errors.New("target already exists")
)

// Entry 是一条目录项。
type Entry struct {
	Name string
	Dir  bool
	Size int64
	Mod  time.Time
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
	Open(ctx context.Context, p string) (webdav.File, error)
}

// Manager 是 Files 的实现。
type Manager struct {
	fs webdav.FileSystem
	// mu 串行化变更操作：面板上连点两下不会互相踩改名/删除
	mu sync.Mutex
}

// New 创建文件管理门面。
//
// 输入：fsys 底层文件系统（通常是 dav.FileSystem）
// 输出：*Manager
// 预期：只做路径规范化与组合，不重复实现存储逻辑
func New(fsys webdav.FileSystem) *Manager { return &Manager{fs: fsys} }

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
	Path string
	Dir  bool
	Size int64
	Mod  time.Time
}

// Searcher 是底层文件系统的可选能力：索引级递归搜索。
//
// dav.FileSystem 实现了它；没有实现的实现会退化成当前目录过滤。
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

// dirRemover 是「删目录记录」的可选能力。
//
// dav.FileSystem 在子项删光后无法再把路径推断成目录，
// 需要直接删 dirs 表的实现；没有实现时退回 RemoveAll。
type dirRemover interface {
	RemoveDir(ctx context.Context, p string) error
}

// removeDir 删除目录记录，优先用底层的 RemoveDir。
func (m *Manager) removeDir(ctx context.Context, p string) error {
	if dr, ok := m.fs.(dirRemover); ok {
		return dr.RemoveDir(ctx, p)
	}
	return m.fs.RemoveAll(ctx, p)
}

// Remove 删除文件或目录。
//
// 输入：p 路径
// 输出：error
// 预期：目录递归删除（先文件、再深层目录），卷根拒绝
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
	fi, err := m.fs.Stat(ctx, c)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return m.fs.RemoveAll(ctx, c)
	}
	dirs, files, err := m.walk(ctx, c)
	if err != nil {
		return err
	}
	for _, f := range files {
		if err := m.fs.RemoveAll(ctx, f); err != nil {
			return err
		}
	}
	// 先删深的目录记录，最后删目标自身（walk 已把目标放进 dirs，这里跳过）
	slices.SortFunc(dirs, func(a, b string) int { return cmp.Compare(b, a) })
	for _, d := range dirs {
		if d == c {
			continue
		}
		if err := m.removeDir(ctx, d); err != nil {
			return err
		}
	}
	return m.removeDir(ctx, c)
}

// Move 移动/改名，文件和目录都支持。
//
// 输入：from 源；to 目标（包含新名字的完整路径）
// 输出：error；目标已存在返回 ErrExists
// 预期：
//   - 文件：改 caption + 迁索引
//   - 目录：逐个文件改名后迁移目录记录（每个文件一次 API 调用）
//   - 中途失败会留下部分已移动的文件，错误原样返回
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
	m.mu.Lock()
	defer m.mu.Unlock()
	fi, err := m.fs.Stat(ctx, src)
	if err != nil {
		return err
	}
	if _, serr := m.fs.Stat(ctx, dst); serr == nil {
		return ErrExists
	}
	if !fi.IsDir() {
		return m.fs.Rename(ctx, src, dst)
	}
	// 目录：不能把一个目录移进它自己的子目录
	if strings.HasPrefix(dst+"/", src+"/") {
		return ErrBadPath
	}
	dirs, files, err := m.walk(ctx, src)
	if err != nil {
		return err
	}
	if err := m.fs.Mkdir(ctx, dst, 0o755); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	for _, f := range files {
		rel, rerr := relPath(src, f)
		if rerr != nil {
			return rerr
		}
		if err := m.fs.Rename(ctx, f, path.Join(dst, rel)); err != nil {
			return err
		}
	}
	// 目录记录：先建新树，再删旧树（旧树此时已空）
	for _, d := range dirs {
		rel, rerr := relPath(src, d)
		if rerr != nil {
			return rerr
		}
		if err := m.fs.Mkdir(ctx, path.Join(dst, rel), 0o755); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
	}
	slices.SortFunc(dirs, func(a, b string) int { return cmp.Compare(b, a) })
	for _, d := range dirs {
		if d == src {
			continue
		}
		if err := m.removeDir(ctx, d); err != nil {
			return err
		}
	}
	return m.removeDir(ctx, src)
}

// Open 以只读方式打开文件，供下载流式读取。
func (m *Manager) Open(ctx context.Context, p string) (webdav.File, error) {
	c, err := clean(p)
	if err != nil {
		return nil, err
	}
	return m.fs.OpenFile(ctx, c, os.O_RDONLY, 0)
}

// walk 递归收集目录下的所有子目录与文件（绝对路径）。
//
// 输入：dir 起始目录
// 输出：dirs 含起始目录自身；files 全文件路径
// 预期：只读遍历，不改变任何状态
func (m *Manager) walk(ctx context.Context, dir string) (dirs, files []string, err error) {
	entries, err := m.List(ctx, dir)
	if err != nil {
		return nil, nil, err
	}
	dirs = append(dirs, dir)
	for _, e := range entries {
		p := path.Join(dir, e.Name)
		if e.Dir {
			sub, subFiles, werr := m.walk(ctx, p)
			if werr != nil {
				return nil, nil, werr
			}
			dirs = append(dirs, sub...)
			files = append(files, subFiles...)
			continue
		}
		files = append(files, p)
	}
	return dirs, files, nil
}

// relPath 求 p 相对 base 的路径。
//
// 输入：base 目录；p 其下某个路径（都已规范化）
// 输出：相对路径；不在 base 之下返回 ErrBadPath
// 预期：只处理斜杠路径，不用 filepath（Windows 反斜杠不参与）
func relPath(base, p string) (string, error) {
	if p == base {
		return "", nil
	}
	prefix := base
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	if !strings.HasPrefix(p, prefix) {
		return "", ErrBadPath
	}
	return strings.TrimPrefix(p, prefix), nil
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
