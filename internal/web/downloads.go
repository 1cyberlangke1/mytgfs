package web

// 下载目录：把卷内文件复制到本地磁盘，并列出已下载文件。
//
// 文件写入由服务端完成，浏览器只发一次请求；复制中断只留下临时文件，
// 不会把半份内容当成成品。目录位置由 [downloads].dir 决定，缺省 data/downloads。

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
)

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

// downloadStore 管理下载目录。
//
// open 按卷名取文件后端；dir 为空表示功能不可用（只读模式）。
type downloadStore struct {
	dir  string
	open func(volume string) (fm.Files, bool)
	// mu 串行化写入与命名去重，避免同一瞬间两次下载抢同一个文件名
	mu sync.Mutex
}

// newDownloadStore 创建下载目录管理器。
//
// 输入：dir 下载目录；open 卷名到文件后端的解析函数
// 输出：*downloadStore
// 预期：只保存参数，不创建目录；首次写入或打开时才建目录
func newDownloadStore(dir string, open func(string) (fm.Files, bool)) *downloadStore {
	return &downloadStore{dir: dir, open: open}
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

// Save 把卷内文件复制到下载目录。
//
// 输入：ctx；vol 卷名；p 卷内绝对路径
// 输出：落盘后的条目
// 预期：
//   - 同名文件已存在时加 " (1)" 后缀，不覆盖
//   - 中途失败删除临时文件，目录里不会出现半份成品
func (d *downloadStore) Save(ctx context.Context, vol, p string) (downloadItem, error) {
	if d == nil || d.dir == "" || d.open == nil {
		return downloadItem{}, errDownloadUnavailable
	}
	if !strings.HasPrefix(p, "/") || strings.Contains(p, "..") {
		return downloadItem{}, errDownloadPath
	}
	m, ok := d.open(vol)
	if !ok {
		return downloadItem{}, errDownloadVolume
	}
	src, err := m.Open(ctx, p)
	if err != nil {
		return downloadItem{}, err
	}
	defer src.Close()

	d.mu.Lock()
	defer d.mu.Unlock()

	if err := os.MkdirAll(d.dir, 0o700); err != nil {
		return downloadItem{}, err
	}
	name := safeDownloadName(path.Base(p))
	target := d.uniquePath(name)
	tmp, err := os.CreateTemp(d.dir, ".mytgfs-dl-*")
	if err != nil {
		return downloadItem{}, err
	}
	tmpName := tmp.Name()
	cleanup := func() {
		tmp.Close()
		os.Remove(tmpName)
	}
	if _, err := io.Copy(tmp, src); err != nil {
		cleanup()
		return downloadItem{}, err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return downloadItem{}, err
	}
	if err := os.Rename(tmpName, target); err != nil {
		os.Remove(tmpName)
		return downloadItem{}, err
	}
	st, err := os.Stat(target)
	if err != nil {
		return downloadItem{}, err
	}
	return downloadItem{Name: filepath.Base(target), Size: st.Size(), Mod: st.ModTime().Format(time.RFC3339)}, nil
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
	return openDir(d.Dir())
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

// handleAPIDownloads 列出、写入、删除下载目录里的文件。
//
// 输入：
//   - GET：无参
//   - POST {"vol","path"}：把卷内文件复制到下载目录
//   - DELETE ?name=：删除本地副本
//
// 输出：GET {"dir","files"}；POST {"dir","saved"}；DELETE {"removed":true}
// 预期：卷后端或目录未配置时返回 501；参数不合法 400；复制失败 502
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
			replyJSON(w, http.StatusOK, map[string]any{"dir": dl.Dir(), "files": files})
		case http.MethodPost:
			var in struct {
				Vol  string `json:"vol"`
				Path string `json:"path"`
			}
			if !decodeBody(w, r, &in) {
				return
			}
			item, err := dl.Save(r.Context(), strings.TrimSpace(in.Vol), strings.TrimSpace(in.Path))
			if err != nil {
				replyJSONError(w, downloadStatus(err), err.Error())
				return
			}
			replyJSON(w, http.StatusCreated, map[string]any{"dir": dl.Dir(), "saved": item})
		case http.MethodDelete:
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
// 预期：参数类 400、卷不存在 404、其余 502，便于前端区分提示
func downloadStatus(err error) int {
	switch {
	case errors.Is(err, errDownloadPath), errors.Is(err, errDownloadName):
		return http.StatusBadRequest
	case errors.Is(err, errDownloadVolume):
		return http.StatusNotFound
	case errors.Is(err, errDownloadUnavailable):
		return http.StatusNotImplemented
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
