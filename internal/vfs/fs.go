package vfs

// 卷内虚拟文件系统：把频道内容暴露成文件系统语义。
//
// 远端每条消息自述名字与父消息号，改名/移动只编辑这一条消息，
// 删除按子树批量删消息；本地索引里的 path 只是派生缓存。

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
	"sync"

	"mytgfs/internal/dirent"
	"mytgfs/internal/i18n"
	"mytgfs/internal/index"
	"mytgfs/internal/push"
	"mytgfs/internal/queue"
	"mytgfs/internal/telegramstore"
)

// Store 是本层需要的存储能力。
type Store interface {
	ReadRange(ctx context.Context, p *telegramstore.Part, offset int64, length int, progress func(done int64)) ([]byte, error)
	Upload(ctx context.Context, r io.Reader, size int64, name, mime, caption string) (*telegramstore.Part, error)
	// UploadResumable 按内容 key 断点续传，中断后能跳过已上传分片
	UploadResumable(ctx context.Context, r io.ReaderAt, size int64, key, name, mime, caption string) (*telegramstore.Part, error)
	// Refresh 按消息 ID 重新拉取 document 元数据（file_reference 过期时用）
	Refresh(ctx context.Context, messageID int) (*telegramstore.Part, error)
	// Copy 引用已有 document 发一条新消息，不重传字节（写时去重用）
	Copy(ctx context.Context, src *telegramstore.Part, name, mime, caption string) (*telegramstore.Part, error)
	Delete(ctx context.Context, ids ...int) error

	// 节点消息读写
	SendNode(ctx context.Context, body string) (int, error)
	EditNode(ctx context.Context, id int, body string) error
	ReadNode(ctx context.Context, id int) (string, error)
}

// FileSystem 是卷内的文件系统实现。
type FileSystem struct {
	ctx    context.Context
	idx    *index.Index
	store  Store
	chunk  int64
	volume string
	// q 非空时所有改动频道的操作走持久化队列
	q *queue.Queue
	// tmpDir 是上传临时文件目录；空表示系统临时目录
	tmpDir string
	// tmpOnce 保证每个进程只扫一次旧临时文件
	tmpOnce sync.Once
	// tr 记录正在下载的文件进度；键是卷内路径
	trMu sync.Mutex
	tr   map[string]*Transfer
	// events 是写操作完成后通知前端的通道；构造时必填，忘接就编译不过
	events push.Events
}

// notify 推送一次「卷内容变了」。
//
// 输入：无
// 输出：无
// 预期：由所有会改动频道的路径调用；没有推送通道时（命令行单跑）直接返回
func (d *FileSystem) notify() {
	if d.events == nil {
		return
	}
	d.events.Publish(push.TopicStatus)
}

// New 创建卷内文件系统。
//
// 输入：ctx；idx 索引；store 存储；chunk 读块大小；events 状态推送通道（必填）
// 输出：*FileSystem
// 预期：所有路径都相对于卷根 "/"；events 为 nil 表示当前进程没有面板可通知
func New(ctx context.Context, idx *index.Index, store Store, chunk int64, events push.Events) *FileSystem {
	if chunk <= 0 {
		chunk = 1 << 20
	}
	return &FileSystem{ctx: ctx, idx: idx, store: store, chunk: chunk, events: events}
}

// SetQueue 挂上串行队列（测试可注入内存实现）。
func (d *FileSystem) SetQueue(q *queue.Queue) { d.q = q }

// SetVolume 设置卷名；任务落库时用来区分频道。
func (d *FileSystem) SetVolume(name string) { d.volume = name }

// SetTempDir 指定上传临时文件目录。
func (d *FileSystem) SetTempDir(dir string) { d.tmpDir = dir }

// Queue 返回挂上的队列，未设置时为 nil。
func (d *FileSystem) Queue() *queue.Queue { return d.q }

// Index 返回本地索引，供命令行在写入后回读统计。
func (d *FileSystem) Index() *index.Index { return d.idx }

// nodeAt 按路径取节点。
//
// 输入：ctx；p 卷内路径
// 输出：节点、是否存在、error
// 预期：路径 "/" 是虚拟卷根，不对应任何消息；其余按派生路径查本地索引
func (d *FileSystem) nodeAt(ctx context.Context, p string) (index.Node, bool, error) {
	if clean(p) == "/" {
		return index.Node{IsDir: true, Path: "/"}, true, nil
	}
	return d.idx.NodeByPath(ctx, p)
}

// parentOf 取父目录节点与最后一段名字。
//
// 输入：p 卷内路径（不能是根）
// 输出：父节点（根节点 ParentID/ID 为 0）、名字；父目录不存在时 ok=false
func (d *FileSystem) parentOf(ctx context.Context, p string) (index.Node, string, bool, error) {
	p = clean(p)
	if p == "/" {
		return index.Node{}, "", false, nil
	}
	dir, base := path.Split(p)
	parent, ok, err := d.nodeAt(ctx, dir)
	if err != nil || !ok || !parent.IsDir {
		return index.Node{}, "", false, err
	}
	return parent, base, true, nil
}

// nodeBody 生成节点消息正文；目录无摘要。
//
// 输入：name 名字；parent 父消息号；isDir 是否目录；sha 文件内容摘要
// 输出：JSON 正文；名字超长时返回本地化的 err.name_too_long
func nodeBody(name string, parent int, isDir bool, sha string) (string, error) {
	body, err := dirent.Encode(dirent.Node{Name: name, Parent: parent, Dir: isDir, Sha: sha})
	if errors.Is(err, dirent.ErrNameTooLong) {
		return "", fmt.Errorf("%s", i18n.T("err.name_too_long", dirent.MaxNameBytes))
	}
	return body, err
}

// sendNode 发一条节点消息，把消息号作为节点 id 返回。
func (d *FileSystem) sendNode(ctx context.Context, name string, parent int, isDir bool, sha string) (int, error) {
	body, err := nodeBody(name, parent, isDir, sha)
	if err != nil {
		return 0, err
	}
	return d.store.SendNode(ctx, body)
}

// reconcileNode 以远端节点正文为准纠正本地单条记录。
//
// 输入：ctx；id 节点消息号
// 输出：error
// 预期：
//   - 远端改名/移动已经成功、本地更新失败时，按 id 重读正文并对齐
//   - 正文不是合法节点或消息已删：删掉本地节点，避免视图残留
//   - 不删任何远端消息，纯对账，可重复调用
func (d *FileSystem) reconcileNode(ctx context.Context, id int) error {
	body, err := d.store.ReadNode(ctx, id)
	if err != nil {
		// 远端都读不到：本地这条没有权威依据，删掉
		return d.idx.DeleteNode(ctx, id)
	}
	node, ok := dirent.Parse(body)
	if !ok {
		return d.idx.DeleteNode(ctx, id)
	}
	pid := node.Parent
	if pid != 0 {
		if _, exists, _ := d.idx.NodeByID(ctx, pid); !exists {
			pid = 0
		}
	}
	cur, known, err := d.idx.NodeByID(ctx, id)
	if err != nil {
		return err
	}
	if !known {
		return d.idx.PutNode(ctx, index.Node{
			ID: id, ParentID: pid, Name: node.Name, IsDir: node.Dir,
			Sha256: node.Sha,
		})
	}
	if cur.ParentID != pid || cur.Name != node.Name {
		if _, _, merr := d.idx.MoveNode(ctx, id, pid, node.Name); merr != nil {
			return merr
		}
	}
	return nil
}

// Mkdir 创建目录：发一条目录节点消息，再登记本地索引。
//
// 输入：name 卷内路径
// 输出：error
// 预期：父目录不存在返回 os.ErrNotExist，同名已存在返回 os.ErrExist
func (d *FileSystem) Mkdir(ctx context.Context, name string, _ os.FileMode) error {
	name = clean(name)
	if name == "/" {
		return os.ErrExist
	}
	parent, base, ok, err := d.parentOf(ctx, name)
	if err != nil {
		return err
	}
	if !ok {
		return os.ErrNotExist
	}
	if _, exists, err := d.idx.Child(ctx, parent.ID, base); err != nil {
		return err
	} else if exists {
		return os.ErrExist
	}
	return d.enqueue(ctx, jobMkdir, name, mkdirJob{Parent: parent.ID, Name: base})
}

// Search 在索引里递归搜索文件名。
func (d *FileSystem) Search(ctx context.Context, dir, query string, limit int) ([]index.SearchHit, error) {
	return d.idx.Search(ctx, dir, query, limit)
}

// OpenFile 打开文件或目录。
func (d *FileSystem) OpenFile(ctx context.Context, name string, flag int, perm os.FileMode) (File, error) {
	name = clean(name)
	if flag&(os.O_WRONLY|os.O_RDWR|os.O_CREATE) != 0 {
		tmp, err := d.newTemp()
		if err != nil {
			return nil, err
		}
		return &file{d: d, name: name, tmp: tmp, write: true, perm: perm, sum: sha256.New()}, nil
	}
	n, ok, err := d.nodeAt(ctx, name)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, os.ErrNotExist
	}
	if n.IsDir {
		return &file{d: d, name: name, node: &n, dir: true}, nil
	}
	return &file{d: d, name: name, node: &n}, nil
}

// RemoveAll 删除文件或目录。
//
// 输入：name 卷内路径
// 输出：error
// 预期：批量删掉子树消息与本地记录；根目录不可删
func (d *FileSystem) RemoveAll(ctx context.Context, name string) error {
	name = clean(name)
	if name == "/" {
		return errors.New(i18n.T("err.dav_root_delete"))
	}
	n, ok, err := d.nodeAt(ctx, name)
	if err != nil {
		return err
	}
	if !ok {
		return os.ErrNotExist
	}
	ids, err := d.idx.SubtreeIDs(ctx, n.ID)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	return d.enqueue(ctx, jobRemove, name, removeJob{ID: n.ID})
}

// Rename 改名或移动：只编辑节点自己那条消息。
//
// 输入：oldName 旧路径；newName 新路径
// 输出：error
// 预期：
//   - O(1) 次远端编辑：与子树大小无关
//   - 目录不能移进自己的子树（本地 parent 链判定）
func (d *FileSystem) Rename(ctx context.Context, oldName, newName string) error {
	oldName, newName = clean(oldName), clean(newName)
	if oldName == newName {
		return nil
	}
	if oldName == "/" {
		return errors.New(i18n.T("err.dav_root_rename"))
	}
	src, ok, err := d.nodeAt(ctx, oldName)
	if err != nil {
		return err
	}
	if !ok {
		return os.ErrNotExist
	}
	newParent, newBase, ok, err := d.parentOf(ctx, newName)
	if err != nil {
		return err
	}
	if !ok {
		return os.ErrNotExist
	}
	if src.IsDir {
		inside, derr := d.idx.IsDescendant(ctx, src.ID, newParent.ID)
		if derr != nil {
			return derr
		}
		if inside {
			return errors.New(i18n.T("err.dav_dir_into_self"))
		}
	}
	if existing, exists, err := d.idx.Child(ctx, newParent.ID, newBase); err != nil {
		return err
	} else if exists && existing.ID != src.ID {
		return os.ErrExist
	}
	return d.enqueue(ctx, jobRename, newName, renameJob{ID: src.ID, Parent: newParent.ID, Name: newBase})
}

// NodeBody 生成把已有消息挂到 dst 路径上的节点正文。
//
// 输入：ctx；dst 卷内目标路径；sha 文件内容摘要
// 输出：JSON 正文；error
// 预期：供跨卷复制在发引用消息之前确定 caption，名字/父号一次写对
func (d *FileSystem) NodeBody(ctx context.Context, dst, sha string) (string, error) {
	parent, base, ok, err := d.parentOf(ctx, dst)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", os.ErrNotExist
	}
	return nodeBody(base, parent.ID, false, sha)
}

// Import 把已存在于频道的消息登记到本卷路径。
//
// 输入：dst 目标路径；part 已有消息；sha 内容摘要；mtime 修改时间；overwrite 目标已存在时是否替换
// 输出：error
// 预期：只写本地节点记录，不发送任何消息字节（跨卷复制用，节点正文由调用方写进消息）
func (d *FileSystem) Import(ctx context.Context, dst string, part *telegramstore.Part, sha string, mtime int64, overwrite bool) error {
	dst = clean(dst)
	parent, base, ok, err := d.parentOf(ctx, dst)
	if err != nil {
		return err
	}
	if !ok {
		return os.ErrNotExist
	}
	if _, exists, cerr := d.idx.Child(ctx, parent.ID, base); cerr != nil {
		return cerr
	} else if exists {
		if !overwrite {
			return os.ErrExist
		}
		if rerr := d.RemoveAll(ctx, dst); rerr != nil {
			return rerr
		}
	}
	if err := d.idx.PutNode(ctx, index.Node{
		ID: part.MessageID, ParentID: parent.ID, Name: base, Path: dst,
		DocumentID: part.DocumentID, AccessHash: part.AccessHash,
		FileReference: part.FileReference, Size: part.Size, Mtime: mtime, Sha256: sha,
	}); err != nil {
		return err
	}
	d.notify()
	return nil
}

// Stat 返回文件或目录信息。
func (d *FileSystem) Stat(ctx context.Context, name string) (fs.FileInfo, error) {
	n, ok, err := d.nodeAt(ctx, name)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, os.ErrNotExist
	}
	if n.IsDir {
		return info{name: path.Base(name), dir: true, mod: unixTime(n.Mtime)}, nil
	}
	return info{name: path.Base(name), size: n.Size, mod: unixTime(n.Mtime)}, nil
}

// dirPathOf 返回父目录的派生路径；卷根返回 "/"。
func dirPathOf(ctx context.Context, idx *index.Index, parentID int) string {
	if parentID <= 0 {
		return "/"
	}
	if n, ok, err := idx.NodeByID(ctx, parentID); err == nil && ok {
		return n.Path
	}
	return "/"
}

// joinPath 拼父路径与名字。
func joinPath(parent, name string) string {
	if parent == "" || parent == "/" {
		return "/" + name
	}
	return parent + "/" + name
}

// clean 规范化路径。
func clean(p string) string {
	p = strings.ReplaceAll(p, `\`, "/")
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return path.Clean(p)
}

// partOf 把节点记录转成下载所需元数据。
func partOf(n index.Node) *telegramstore.Part {
	return &telegramstore.Part{
		MessageID:     n.ID,
		DocumentID:    n.DocumentID,
		AccessHash:    n.AccessHash,
		FileReference: n.FileReference,
		Size:          n.Size,
	}
}
