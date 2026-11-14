package web

// 下载目录：把卷内文件复制到本地磁盘，并列出已下载文件。
//
// 复制在后台任务里进行：请求立刻返回任务，进度通过推送通道节流上报，
// 支持暂停（保留已落盘字节）、继续（从断点续写）与取消。
// 中断只留下临时文件，不会把半份内容当成成品。

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"mytgfs/internal/config"
	"mytgfs/internal/fm"
	"mytgfs/internal/i18n"
	"mytgfs/internal/push"
)

var (
	// errDownloadUnavailable 表示没有可用的卷后端，下载功能整体不可用。
	errDownloadUnavailable = errors.New(i18n.T("err.downloads_unavailable"))
	// errDownloadVolume 表示请求里的卷名不在配置中。
	errDownloadVolume = errors.New(i18n.T("err.downloads_no_volume"))
	// errDownloadPath 表示卷内路径越界或不是绝对路径。
	errDownloadPath = errors.New(i18n.T("err.downloads_bad_path"))
	// errDownloadName 表示已下载文件名不合法（含路径分隔符或空名）。
	errDownloadName = errors.New(i18n.T("err.downloads_bad_name"))
	// errDownloadTask 表示任务不存在或状态不允许该操作。
	errDownloadTask = errors.New(i18n.T("err.downloads_task"))
)

// 下载任务状态。
const (
	downloadRunning = "running"
	downloadPaused  = "paused"
	downloadFailed  = "failed"
)

// downloadChunk 是单次读写大小：越大系统调用越少，越小进度越细。
const downloadChunk = 256 << 10

// resolveDownloadsDir 把配置值解析成绝对路径。
//
// 输入：configPath 配置文件路径；dir 原始配置（空走缺省）
// 输出：绝对路径
// 预期：相对路径按配置文件所在目录解析，托盘从任意工作目录启动也不会写错位置
func resolveDownloadsDir(configPath, dir string) string {
	if strings.TrimSpace(dir) == "" {
		dir = config.DefaultDir
	}
	if filepath.IsAbs(dir) {
		return dir
	}
	base, err := filepath.Abs(filepath.Dir(configPath))
	if err != nil {
		return dir
	}
	return filepath.Join(base, dir)
}

// downloadItem 是已下载文件在界面上的展示结构。
type downloadItem struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	// Mod 是最后写入时间的 RFC3339 文本，前端按本地时区展示。
	Mod string `json:"mod"`
}

// downloadTask 是一次进行中的下载。
type downloadTask struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Vol  string `json:"vol"`
	Path string `json:"path"`
	Size int64  `json:"size"`
	Done int64  `json:"done"`
	// State 取 running/paused/failed；完成的任务直接从列表移除，
	// 由 downloads 文件列表接管展示。
	State string `json:"state"`
	Err   string `json:"error,omitempty"`

	cancel context.CancelFunc
	tmp    string
	// stop 在旧协程真正退出后关闭：继续时先等它走完，避免两个协程同时写同一个临时文件
	stop chan struct{}
}

// clone 返回可安全返回给 HTTP 层的副本。
func (t *downloadTask) clone() downloadTask {
	return downloadTask{
		ID: t.ID, Name: t.Name, Vol: t.Vol, Path: t.Path,
		Size: t.Size, Done: t.Done, State: t.State, Err: t.Err,
	}
}

// downloadStore 管理下载目录与后台下载任务。
//
// open 按卷名取文件后端；dir 为空表示功能不可用（只读模式）。
type downloadStore struct {
	dir    string
	open   func(volume string) (fm.Files, bool)
	events push.Events
	base   context.Context
	// mu 同时保护目录命名与任务表，避免同一瞬间两次下载抢同一个文件名
	mu    sync.Mutex
	tasks map[string]*downloadTask
	order []string
}

// newDownloadStore 创建下载目录管理器。
//
// 输入：dir 下载目录；open 卷名到文件后端的解析函数；events 进度推送通道（可为 nil）
// 输出：*downloadStore
// 预期：只保存参数，不创建目录；首次写入或打开时才建目录
func newDownloadStore(dir string, open func(string) (fm.Files, bool), events push.Events) *downloadStore {
	return &downloadStore{
		dir: dir, open: open, events: events,
		base:  context.Background(),
		tasks: make(map[string]*downloadTask),
	}
}

// Dir 返回下载目录的绝对路径。
//
// 输入：无
// 输出：绝对路径；目录为空时返回空串
// 预期：仅用于界面展示与打开目录，不负责建目录
func (d *downloadStore) Dir() string {
	if d == nil || d.dir == "" {
		return ""
	}
	if abs, err := filepath.Abs(d.dir); err == nil {
		return abs
	}
	return d.dir
}

// notify 推一次「状态变了」。
//
// 输入：无
// 输出：无
// 预期：通道由 Hub 节流到 400ms，逐块调用也不会打爆前端
func (d *downloadStore) notify() {
	if d.events == nil {
		return
	}
	d.events.Publish(push.TopicStatus)
}

// Start 建一个后台下载任务，立即返回。
//
// 输入：vol 卷名；p 卷内绝对路径
// 输出：任务快照
// 预期：
//   - 只做参数校验与临时文件创建，不复制内容
//   - 同名文件在完成时才决定最终名，重名加 " (n)" 后缀
func (d *downloadStore) Start(vol, p string) (downloadTask, error) {
	if d == nil || d.dir == "" || d.open == nil {
		return downloadTask{}, errDownloadUnavailable
	}
	if !strings.HasPrefix(p, "/") || strings.Contains(p, "..") {
		return downloadTask{}, errDownloadPath
	}
	m, ok := d.open(vol)
	if !ok {
		return downloadTask{}, errDownloadVolume
	}
	src, err := m.Open(context.Background(), p)
	if err != nil {
		return downloadTask{}, err
	}
	info, err := src.Stat()
	_ = src.Close()
	if err != nil {
		return downloadTask{}, err
	}
	if info.IsDir() {
		return downloadTask{}, errDownloadPath
	}
	if err := os.MkdirAll(d.dir, 0o700); err != nil {
		return downloadTask{}, err
	}
	tmp, err := os.CreateTemp(d.dir, ".mytgfs-dl-*")
	if err != nil {
		return downloadTask{}, err
	}
	tmpName := tmp.Name()
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return downloadTask{}, err
	}

	t := &downloadTask{
		ID:    newToken(8),
		Name:  safeDownloadName(path.Base(p)),
		Vol:   vol,
		Path:  p,
		Size:  info.Size(),
		State: downloadRunning,
		tmp:   tmpName,
	}
	d.mu.Lock()
	d.tasks[t.ID] = t
	d.order = append(d.order, t.ID)
	d.mu.Unlock()

	d.launch(t)
	d.notify()
	return t.clone(), nil
}

// launch 启动复制协程并登记退出信号。
//
// 输入：任务
// 输出：无
// 预期：协程退出时关闭 stop；Resume 据此确认旧协程已经停手
func (d *downloadStore) launch(t *downloadTask) {
	stop := make(chan struct{})
	d.mu.Lock()
	t.stop = stop
	d.mu.Unlock()
	go func() {
		defer close(stop)
		d.run(t)
	}()
}

// Pause 暂停一个正在跑的任务。
//
// 输入：id 任务 ID
// 输出：error；任务不存在或已暂停时返回 errDownloadTask
// 预期：已写入磁盘的字节保留，继续时从断点开始读
func (d *downloadStore) Pause(id string) error {
	d.mu.Lock()
	t, ok := d.tasks[id]
	if !ok || t.State != downloadRunning {
		d.mu.Unlock()
		return errDownloadTask
	}
	t.State = downloadPaused
	cancel := t.cancel
	d.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	d.notify()
	return nil
}

// Resume 继续一个暂停的任务。
//
// 输入：id 任务 ID
// 输出：error；任务不存在或不在暂停态时返回 errDownloadTask
// 预期：重新打开源文件并 Seek 到已下载字节，不重传已完成部分
func (d *downloadStore) Resume(id string) error {
	d.mu.Lock()
	t, ok := d.tasks[id]
	if !ok || t.State != downloadPaused {
		d.mu.Unlock()
		return errDownloadTask
	}
	t.State = downloadRunning
	prev := t.stop
	d.mu.Unlock()

	// 等旧协程真正退出再续写：cancel 只是请求，读写可能还在进行
	go func() {
		if prev != nil {
			<-prev
		}
		d.launch(t)
	}()
	d.notify()
	return nil
}

// Cancel 取消任务并删除临时文件。
//
// 输入：id 任务 ID
// 输出：error；任务不存在时返回 errDownloadTask
// 预期：取消后不留半份临时文件，也不出现在任务列表里
func (d *downloadStore) Cancel(id string) error {
	d.mu.Lock()
	t, ok := d.tasks[id]
	if !ok {
		d.mu.Unlock()
		return errDownloadTask
	}
	delete(d.tasks, id)
	d.order = slices.DeleteFunc(d.order, func(x string) bool { return x == id })
	cancel := t.cancel
	tmp := t.tmp
	d.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if tmp != "" {
		_ = os.Remove(tmp)
	}
	d.notify()
	return nil
}

// Tasks 返回当前任务快照，按开始顺序。
//
// 输入：无
// 输出：任务副本；没有任务时返回空切片
// 预期：读取方拿不到 cancel/tmp 等内部字段
func (d *downloadStore) Tasks() []downloadTask {
	if d == nil {
		return []downloadTask{}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]downloadTask, 0, len(d.order))
	for _, id := range d.order {
		if t, ok := d.tasks[id]; ok {
			out = append(out, t.clone())
		}
	}
	return out
}

// run 执行复制：分块读写、逐块上报进度、完成时原子改名。
//
// 输入：任务
// 输出：无
// 预期：
//   - 暂停/取消导致的读错误按当前状态处理，不记成失败
//   - 成功后才把临时文件改名成最终文件
func (d *downloadStore) run(t *downloadTask) {
	ctx, cancel := context.WithCancel(d.base)
	d.mu.Lock()
	t.cancel = cancel
	offset := t.Done
	d.mu.Unlock()
	defer cancel()

	fail := func(err error) {
		d.mu.Lock()
		// 暂停/取消已经把状态改掉，这里不覆盖
		if t.State == downloadRunning {
			t.State = downloadFailed
			t.Err = err.Error()
		}
		d.mu.Unlock()
		d.notify()
	}

	m, ok := d.open(t.Vol)
	if !ok {
		fail(errDownloadVolume)
		return
	}
	src, err := m.Open(ctx, t.Path)
	if err != nil {
		fail(err)
		return
	}
	defer src.Close()
	if offset > 0 {
		if _, err := src.Seek(offset, io.SeekStart); err != nil {
			fail(err)
			return
		}
	}
	f, err := os.OpenFile(t.tmp, os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		fail(err)
		return
	}
	// 续传必须把写指针也移到断点，否则会从头覆盖已下载的内容
	if offset > 0 {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			_ = f.Close()
			fail(err)
			return
		}
	}
	buf := make([]byte, downloadChunk)
	for {
		n, rerr := src.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				_ = f.Close()
				fail(werr)
				return
			}
			d.mu.Lock()
			t.Done += int64(n)
			d.mu.Unlock()
			d.notify()
		}
		if errors.Is(rerr, io.EOF) {
			break
		}
		if rerr != nil {
			_ = f.Close()
			d.mu.Lock()
			paused := t.State != downloadRunning
			d.mu.Unlock()
			if paused || ctx.Err() != nil {
				return
			}
			fail(rerr)
			return
		}
	}
	if err := f.Close(); err != nil {
		fail(err)
		return
	}
	d.finalize(t)
}

// finalize 把临时文件改名成最终文件并移除任务。
//
// 输入：已复制完成的任务
// 输出：无
// 预期：改名失败按失败处理并保留临时文件，便于排查
func (d *downloadStore) finalize(t *downloadTask) {
	d.mu.Lock()
	target := d.uniquePath(t.Name)
	if err := os.Rename(t.tmp, target); err != nil {
		t.State = downloadFailed
		t.Err = err.Error()
		d.mu.Unlock()
		d.notify()
		return
	}
	delete(d.tasks, t.ID)
	d.order = slices.DeleteFunc(d.order, func(x string) bool { return x == t.ID })
	d.mu.Unlock()
	d.notify()
}

// List 列出下载目录里的文件，最近写入的在前。
//
// 输入：无
// 输出：文件条目；目录不存在时返回空列表
// 预期：跳过子目录与临时文件（以 . 开头）
func (d *downloadStore) List() ([]downloadItem, error) {
	if d == nil || d.dir == "" {
		return nil, errDownloadUnavailable
	}
	entries, err := os.ReadDir(d.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []downloadItem{}, nil
		}
		return nil, err
	}
	out := make([]downloadItem, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, downloadItem{
			Name: e.Name(),
			Size: info.Size(),
			Mod:  info.ModTime().Format(time.RFC3339),
		})
	}
	slicesSortDescByMod(out)
	return out, nil
}

// Remove 删除下载目录里的一个文件。
//
// 输入：name 文件名（不含路径）
// 输出：error；文件名含分隔符或 .. 时拒绝
// 预期：只删本地副本，云端内容不受影响
func (d *downloadStore) Remove(name string) error {
	if d == nil || d.dir == "" {
		return errDownloadUnavailable
	}
	if name == "" || name != filepath.Base(name) || strings.Contains(name, "..") {
		return errDownloadName
	}
	return os.Remove(filepath.Join(d.dir, name))
}

// Open 用系统文件管理器打开下载目录。
//
// 输入：无
// 输出：error；目录不存在时先创建
// 预期：Windows 资源管理器 / macOS Finder / Linux 文件管理器
func (d *downloadStore) Open() error {
	if d == nil || d.dir == "" {
		return errDownloadUnavailable
	}
	if err := os.MkdirAll(d.dir, 0o700); err != nil {
		return err
	}
	return openPath(d.Dir())
}

// uniquePath 返回目录里不冲突的目标路径。
//
// 输入：name 期望文件名
// 输出：完整路径；已存在时在扩展名前插入 " (n)"
// 预期：保留扩展名，重名后缀从 1 递增，不覆盖已有文件
func (d *downloadStore) uniquePath(name string) string {
	target := filepath.Join(d.dir, name)
	if _, err := os.Stat(target); errors.Is(err, os.ErrNotExist) {
		return target
	}
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for i := 1; ; i++ {
		cand := filepath.Join(d.dir, base+" ("+strconv.Itoa(i)+")"+ext)
		if _, err := os.Stat(cand); errors.Is(err, os.ErrNotExist) {
			return cand
		}
	}
}

// safeDownloadName 把远端文件名收敛成各平台都能落盘的名字。
//
// 输入：name 远端 basename
// 输出：去掉非法字符后的文件名
// 预期：Windows 的 \ / : * ? " < > | 与控制字符替换成 _；空名回落到 download
func safeDownloadName(name string) string {
	name = strings.TrimSpace(name)
	var b strings.Builder
	for _, r := range name {
		switch {
		case r < 0x20 || strings.ContainsRune(`<>:"/\|?*`, r):
			b.WriteByte('_')
		default:
			b.WriteRune(r)
		}
	}
	out := strings.TrimRight(b.String(), ". ")
	if out == "" {
		return "download"
	}
	return out
}

// Path 把已下载文件名解析成磁盘路径，并确认文件存在。
//
// 输入：name 文件名（不含路径）
// 输出：绝对路径
// 预期：含分隔符/.. 的名字一律拒绝，避免越出下载目录
func (d *downloadStore) Path(name string) (string, error) {
	if d == nil || d.dir == "" {
		return "", errDownloadUnavailable
	}
	if name == "" || name != filepath.Base(name) || strings.Contains(name, "..") {
		return "", errDownloadName
	}
	p := filepath.Join(d.dir, name)
	info, err := os.Stat(p)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", errDownloadName
	}
	return p, nil
}

// OpenFile 用系统默认程序打开一个已下载文件。
//
// 输入：name 文件名
// 输出：error；文件不存在或启动失败时返回
// 预期：桌面壳里双击行为一致，不在服务端预览
func (d *downloadStore) OpenFile(name string) error {
	p, err := d.Path(name)
	if err != nil {
		return err
	}
	return openPath(p)
}

// handleAPIDownloadFile 把已下载文件本身提供给浏览器/桌面壳预览。
//
// 输入：GET/HEAD ?name=
// 输出：文件内容；支持 Range，可直接喂给 video/audio/iframe
// 预期：inline 展示，不触发另存为；文件名非法或不存在返回 400/404
func handleAPIDownloadFile(dl *downloadStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			replyJSONError(w, http.StatusMethodNotAllowed, i18n.T("err.web_method"))
			return
		}
		p, err := dl.Path(r.URL.Query().Get("name"))
		if err != nil {
			replyJSONError(w, downloadStatus(err), err.Error())
			return
		}
		f, err := os.Open(p)
		if err != nil {
			replyJSONError(w, http.StatusNotFound, err.Error())
			return
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil {
			replyJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		http.ServeContent(w, r, info.Name(), info.ModTime(), f)
	}
}

// handleAPIDownloadOpenFile 用系统默认程序打开已下载文件。
//
// 输入：POST ?name=
// 输出：{\"opened\":true}
// 预期：失败返回 502，由页面提示原因
func handleAPIDownloadOpenFile(dl *downloadStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			replyJSONError(w, http.StatusMethodNotAllowed, i18n.T("err.web_method"))
			return
		}
		if err := dl.OpenFile(r.URL.Query().Get("name")); err != nil {
			replyJSONError(w, downloadStatus(err), err.Error())
			return
		}
		replyJSON(w, http.StatusOK, map[string]any{"opened": true})
	}
}

// handleAPIDownloads 列出、启动、删除下载任务与本地文件。
//
// 输入：
//   - GET：无参，返回目录、文件与任务
//   - POST {"vol","path"}：启动后台下载，立即返回任务
//   - DELETE ?id=：取消任务；?name=：删除本地副本
//
// 输出：GET {"dir","files","tasks"}；POST 202 {"task"}；DELETE {"removed":true}
// 预期：卷后端或目录未配置时返回 501；参数不合法 400
func handleAPIDownloads(dl *downloadStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if dl == nil || dl.dir == "" {
			replyJSONError(w, http.StatusNotImplemented, i18n.T("err.downloads_unavailable"))
			return
		}
		switch r.Method {
		case http.MethodGet:
			files, err := dl.List()
			if err != nil {
				replyJSONError(w, http.StatusInternalServerError, i18n.T("err.downloads_list"))
				return
			}
			replyJSON(w, http.StatusOK, map[string]any{"dir": dl.Dir(), "files": files, "tasks": dl.Tasks()})
		case http.MethodPost:
			var in struct {
				Vol  string `json:"vol"`
				Path string `json:"path"`
			}
			if !decodeBody(w, r, &in) {
				return
			}
			task, err := dl.Start(strings.TrimSpace(in.Vol), strings.TrimSpace(in.Path))
			if err != nil {
				replyJSONError(w, downloadStatus(err), err.Error())
				return
			}
			replyJSON(w, http.StatusAccepted, map[string]any{"task": task})
		case http.MethodDelete:
			if id := r.URL.Query().Get("id"); id != "" {
				if err := dl.Cancel(id); err != nil {
					replyJSONError(w, downloadStatus(err), err.Error())
					return
				}
				replyJSON(w, http.StatusOK, map[string]any{"removed": true})
				return
			}
			name := r.URL.Query().Get("name")
			if err := dl.Remove(name); err != nil {
				replyJSONError(w, downloadStatus(err), err.Error())
				return
			}
			replyJSON(w, http.StatusOK, map[string]any{"removed": true})
		default:
			replyJSONError(w, http.StatusMethodNotAllowed, i18n.T("err.web_method"))
		}
	}
}

// handleAPIDownloadTask 暂停或继续一个下载任务。
//
// 输入：POST {"id","action"}，action 取 pause/resume
// 输出：{"ok":true}；任务不存在或状态不符 409
// 预期：暂停保留已下载字节，继续从断点续写
func handleAPIDownloadTask(dl *downloadStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			replyJSONError(w, http.StatusMethodNotAllowed, i18n.T("err.web_method"))
			return
		}
		if dl == nil || dl.dir == "" {
			replyJSONError(w, http.StatusNotImplemented, i18n.T("err.downloads_unavailable"))
			return
		}
		var in struct {
			ID     string `json:"id"`
			Action string `json:"action"`
		}
		if !decodeBody(w, r, &in) {
			return
		}
		var err error
		switch in.Action {
		case "pause":
			err = dl.Pause(strings.TrimSpace(in.ID))
		case "resume":
			err = dl.Resume(strings.TrimSpace(in.ID))
		default:
			replyJSONError(w, http.StatusBadRequest, i18n.T("err.downloads_bad_request"))
			return
		}
		if err != nil {
			replyJSONError(w, downloadStatus(err), err.Error())
			return
		}
		replyJSON(w, http.StatusOK, map[string]any{"ok": true})
	}
}

// handleAPIDownloadsOpen 用系统文件管理器打开下载目录。
//
// 输入：POST 无 body
// 输出：{"opened":true}
// 预期：目录不存在时先创建；打开失败返回 502
func handleAPIDownloadsOpen(dl *downloadStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			replyJSONError(w, http.StatusMethodNotAllowed, i18n.T("err.web_method"))
			return
		}
		if err := dl.Open(); err != nil {
			replyJSONError(w, http.StatusBadGateway, err.Error())
			return
		}
		replyJSON(w, http.StatusOK, map[string]any{"opened": true})
	}
}

// downloadStatus 把下载错误映射成 HTTP 状态码。
//
// 输入：error
// 输出：状态码
// 预期：参数类 400、卷/任务不存在 404 或 409、其余 502
func downloadStatus(err error) int {
	switch {
	case errors.Is(err, errDownloadPath), errors.Is(err, errDownloadName):
		return http.StatusBadRequest
	case errors.Is(err, errDownloadVolume):
		return http.StatusNotFound
	case errors.Is(err, errDownloadTask):
		return http.StatusConflict
	case errors.Is(err, errDownloadUnavailable):
		return http.StatusNotImplemented
	case errors.Is(err, os.ErrNotExist):
		return http.StatusNotFound
	default:
		return http.StatusBadGateway
	}
}

// slicesSortDescByMod 按修改时间倒序排列，时间相同按名字升序。
//
// 输入：items 待排序条目
// 输出：原地排序
// 预期：最近下载的排在最前
func slicesSortDescByMod(items []downloadItem) {
	slices.SortFunc(items, func(a, b downloadItem) int {
		if a.Mod != b.Mod {
			return strings.Compare(b.Mod, a.Mod)
		}
		return strings.Compare(a.Name, b.Name)
	})
}
