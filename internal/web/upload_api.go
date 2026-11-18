package web

// 浏览器分片上传：把一次大文件上传拆成可重试的若干段。
//
// 为什么需要：表单上传一旦连接中断就要从头再来，
// 而回环网络之外的家宽上行经常断。这里把进度留在服务端，
// 断了只重传当前分片；提交时再交给 fm 走正常的入库流程。
//
// 生命周期：
//   POST /api/upload/init    建会话 + 临时文件
//   PUT  /api/upload/chunk   追加一段（offset 必须等于已收字节数）
//   POST /api/upload/commit  提交入库并清理
//   DELETE /api/upload       放弃并清理

import (
	"cmp"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"mytgfs/internal/fm"
	"mytgfs/internal/i18n"
	"mytgfs/internal/upload"
)

// 会话上限与空闲回收时间。
//
// 上限存在的意义：会话持有临时文件句柄，不设上限等于允许匿名占满磁盘。
const (
	uploadMaxSessions = 32
	uploadIdleTimeout = time.Hour
)

// uploadSession 是一次分片上传的状态。
type uploadSession struct {
	mu     sync.Mutex
	id     string
	vol    string
	dir    string
	target string
	size   int64
	// done 是已经落盘的字节数，也是下一段允许的 offset
	done int64
	file *os.File
	// sum 在收分片时累计内容摘要：提交时不用再读一遍整个文件
	sum hash.Hash
	// adopted 表示临时文件已交给文件后端，本会话不再负责删除
	adopted bool
	// lastUsed 是纳秒时间戳：回收扫描与取会话并发读，用原子量避免数据竞争
	lastUsed atomic.Int64
	// busy 表示正在提交：回收协程不得关闭这个句柄
	busy bool
}

// uploadAPI 管理所有分片会话。
type uploadAPI struct {
	manager func(volume string) (fm.Files, bool)
	tmpDir  string
	mu      sync.Mutex
	sess    map[string]*uploadSession
}

// newUploadAPI 创建分片上传接口。
//
// 输入：manager 按卷取文件后端；tmpDir 临时目录（空则系统临时目录）
// 输出：*uploadAPI；manager 为 nil 时返回 nil
// 预期：临时文件放进 tmpDir，异常中断的残留由 dav 的统一清扫处理
func newUploadAPI(manager func(volume string) (fm.Files, bool), tmpDir string) *uploadAPI {
	if manager == nil {
		return nil
	}
	tmpDir = cmp.Or(tmpDir, os.TempDir())
	return &uploadAPI{manager: manager, tmpDir: tmpDir, sess: map[string]*uploadSession{}}
}

// initRequest 是 /api/upload/init 的请求体。
type initRequest struct {
	Vol  string `json:"vol"`
	Dir  string `json:"dir"`
	Rel  string `json:"rel"`
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// handleInit 建会话（POST /api/upload/init）。
//
// 输入：JSON {vol,dir,rel,name,size}
// 输出：JSON {id}；参数非法 400
// 预期：只建空临时文件，不读内容
func (u *uploadAPI) handleInit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req initRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil {
		replyJSONError(w, http.StatusBadRequest, i18n.T("err.upload_bad_request"))
		return
	}
	if req.Size <= 0 {
		replyJSONError(w, http.StatusBadRequest, i18n.T("err.upload_empty"))
		return
	}
	if _, ok := u.manager(req.Vol); !ok {
		replyJSONError(w, http.StatusNotFound, i18n.T("err.fm_no_volume"))
		return
	}
	target, err := uploadTarget(req.Dir, req.Rel, req.Name)
	if err != nil {
		replyJSONError(w, http.StatusBadRequest, i18n.T("err.fm_bad_path"))
		return
	}
	if err := os.MkdirAll(u.tmpDir, 0o700); err != nil {
		replyJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	f, err := os.CreateTemp(u.tmpDir, ".mytgfs-dav-up-*")
	if err != nil {
		replyJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s := &uploadSession{
		id: newToken(12), vol: req.Vol, dir: req.Dir, target: target,
		size: req.Size, file: f, sum: sha256.New(),
	}
	s.lastUsed.Store(time.Now().UnixNano())
	u.mu.Lock()
	u.evictLocked(s.id)
	u.sess[s.id] = s
	u.mu.Unlock()
	replyJSON(w, http.StatusOK, map[string]any{"id": s.id})
}

// handleChunk 追加一段（PUT /api/upload/chunk?id=&offset=）。
//
// 输入：查询串 id/offset；请求体是这一段字节
// 输出：JSON {offset} 表示已收字节数；offset 不匹配返回 409
// 预期：只追加不覆盖，写完立即 fsync 之外的持久化交给内核
func (u *uploadAPI) handleChunk(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	s, ok := u.get(r.URL.Query().Get("id"))
	if !ok {
		replyJSONError(w, http.StatusNotFound, i18n.T("err.upload_no_session"))
		return
	}
	offset, err := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
	if err != nil {
		replyJSONError(w, http.StatusBadRequest, i18n.T("err.upload_bad_request"))
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.busy {
		// 提交正在读这个临时文件：再写会让读到的字节和声明大小对不上
		replyJSONError(w, http.StatusConflict, i18n.T("err.upload_busy"))
		return
	}
	if offset != s.done {
		replyJSON(w, http.StatusConflict, map[string]any{
			"offset": s.done, "error": i18n.T("err.upload_offset"),
		})
		return
	}
	// 只允许写满 init 声明的大小：否则单个分片就能把临时文件撑到任意大
	remaining := s.size - s.done
	if remaining <= 0 {
		replyJSONError(w, http.StatusBadRequest, i18n.T("err.upload_incomplete"))
		return
	}
	body := http.MaxBytesReader(w, r.Body, remaining)
	n, cerr := io.Copy(io.MultiWriter(s.file, s.sum), body)
	// 先记已写字节：超限时 MaxBytesReader 已经把磁盘填到声明大小，
	// 不记下来会让下一段的 offset 校验对着错误的进度比
	s.done += n
	s.lastUsed.Store(time.Now().UnixNano())
	if cerr != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(cerr, &tooLarge) {
			replyJSONError(w, http.StatusBadRequest, i18n.T("err.upload_too_large"))
			return
		}
		replyJSONError(w, http.StatusInternalServerError, cerr.Error())
		return
	}
	replyJSON(w, http.StatusOK, map[string]any{"offset": s.done})
}

// handleCommit 提交入库（POST /api/upload/commit?id=）。
//
// 输入：查询串 id
// 输出：JSON {path}；文件后端出错返回 400
// 预期：大小与 init 声明不符时拒绝，避免半截文件入库
func (u *uploadAPI) handleCommit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	s, ok := u.get(r.URL.Query().Get("id"))
	if !ok {
		replyJSONError(w, http.StatusNotFound, i18n.T("err.upload_no_session"))
		return
	}
	s.mu.Lock()
	if s.busy {
		// 同一个会话只允许一个提交在跑：否则两条协程会同时读同一个临时文件
		s.mu.Unlock()
		replyJSONError(w, http.StatusConflict, i18n.T("err.upload_busy"))
		return
	}
	done, size := s.done, s.size
	s.busy = true
	s.mu.Unlock()
	// 提交期间抓住 busy 标记，回收协程不会把正在读的句柄关掉
	defer func() {
		s.mu.Lock()
		s.busy = false
		s.mu.Unlock()
	}()
	if size >= 0 && done != size {
		u.drop(s.id)
		replyJSONError(w, http.StatusBadRequest, i18n.T("err.upload_incomplete"))
		return
	}
	m, ok := u.manager(s.vol)
	if !ok {
		u.drop(s.id)
		replyJSONError(w, http.StatusNotFound, i18n.T("err.fm_no_volume"))
		return
	}
	// 文件夹上传会带上相对路径：先把缺失的父目录补出来，再落文件
	if err := ensureParents(r.Context(), m, s.target); err != nil {
		u.drop(s.id)
		replyJSONError(w, http.StatusBadRequest, fmError(err))
		return
	}
	// 接管临时文件：先关句柄（Windows 下执行体要重新打开），所有权转给文件后端。
	// 摘要在收分片时已经算好，这里不再整文件读一遍。
	s.mu.Lock()
	temp := s.file.Name()
	sha := hex.EncodeToString(s.sum.Sum(nil))
	cerr := s.file.Close()
	s.adopted = cerr == nil
	s.mu.Unlock()
	if cerr != nil {
		u.drop(s.id)
		replyJSONError(w, http.StatusInternalServerError, cerr.Error())
		return
	}
	job, err := m.AdoptUpload(r.Context(), s.target, temp, done, sha)
	if err != nil {
		// 临时文件已交给后端：失败时保留给队列重试，不在会话里删
		u.drop(s.id)
		replyJSONError(w, http.StatusBadRequest, fmError(err))
		return
	}
	u.drop(s.id)
	// job 是后台队列任务号：面板据此跟踪 Telegram 上传的真实字节进度
	replyJSON(w, http.StatusOK, map[string]any{"path": s.target, "job": job})
}

// adoptRequest 是 /api/upload/adopt 的请求体。
//
// Path 是选择器把文件落到本地磁盘后的真实路径（安卓由 Wails 的
// dialogs.OpenFile 复制进应用 cache 后返回）。面板只传路径，不传字节。
type adoptRequest struct {
	Vol  string `json:"vol"`
	Dir  string `json:"dir"`
	Rel  string `json:"rel"`
	Name string `json:"name"`
	Path string `json:"path"`
}

// handleAdopt 接管宿主已落盘的文件（POST /api/upload/adopt）。
//
// 输入：JSON {vol,dir,rel,name,path}，或查询串 vol/dir/rel/name/path
// 输出：JSON {path,job}；路径非法/不可读/空文件返回 400
// 预期：
//   - 字节不经 JS 层：单文件走原生选择器路径，文件夹走 Android fd（/proc/self/fd/N）
//   - 摘要流式计算，不把文件读进内存
//   - 成功后文件所有权交给文件后端，磁盘上的临时副本由它消费
func (u *uploadAPI) handleAdopt(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req adoptRequest
	if q := r.URL.Query(); q.Get("path") != "" {
		// 原生侧直接调用（无请求体）：参数全在 query
		req = adoptRequest{
			Vol:  q.Get("vol"),
			Dir:  q.Get("dir"),
			Rel:  q.Get("rel"),
			Name: q.Get("name"),
			Path: q.Get("path"),
		}
	} else if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil {
		replyJSONError(w, http.StatusBadRequest, i18n.T("err.upload_bad_request"))
		return
	}
	if _, ok := u.manager(req.Vol); !ok {
		replyJSONError(w, http.StatusNotFound, i18n.T("err.fm_no_volume"))
		return
	}
	target, err := uploadTarget(req.Dir, req.Rel, req.Name)
	if err != nil {
		replyJSONError(w, http.StatusBadRequest, i18n.T("err.fm_bad_path"))
		return
	}
	if req.Path == "" || !filepath.IsAbs(req.Path) {
		replyJSONError(w, http.StatusBadRequest, i18n.T("err.upload_bad_request"))
		return
	}
	// /proc/self/fd/N 只保证调用期间有效：先落一份到本层临时目录，
	// 之后队列再慢慢上传。其它路径由调用方保证生命周期，直接接管。
	src := req.Path
	adopted := false
	if strings.HasPrefix(src, "/proc/self/fd/") {
		tmp, _, cpErr := copyFdToTemp(u.tmpDir, src)
		if cpErr != nil {
			replyJSONError(w, http.StatusBadRequest, i18n.T("err.upload_bad_request"))
			return
		}
		src = tmp
		defer func() {
			// 入队成功后所有权交给文件后端，由队列上传完再删；
			// 失败时这里清掉刚复制的临时副本。
			if !adopted {
				_ = os.Remove(tmp)
			}
		}()
	}
	f, err := os.Open(src)
	if err != nil {
		replyJSONError(w, http.StatusBadRequest, i18n.T("err.upload_bad_request"))
		return
	}
	st, statErr := f.Stat()
	if statErr != nil || st.IsDir() || st.Size() <= 0 {
		f.Close()
		replyJSONError(w, http.StatusBadRequest, i18n.T("err.upload_empty"))
		return
	}
	sha, hashErr := upload.Hash(f, st.Size())
	f.Close()
	if hashErr != nil {
		replyJSONError(w, http.StatusBadRequest, i18n.T("err.upload_bad_request"))
		return
	}
	m, _ := u.manager(req.Vol)
	if err := ensureParents(r.Context(), m, target); err != nil {
		replyJSONError(w, http.StatusBadRequest, fmError(err))
		return
	}
	job, err := m.AdoptUpload(r.Context(), target, src, st.Size(), sha)
	if err != nil {
		replyJSONError(w, http.StatusBadRequest, fmError(err))
		return
	}
	adopted = true
	replyJSON(w, http.StatusOK, map[string]any{"path": target, "job": job})
}

// copyFdToTemp 把 /proc/self/fd/N 的内容复制到本层临时目录。
//
// 输入：dir 临时目录；src fd 路径
// 输出：新文件路径、字节数、error
// 预期：调用返回后原 fd 可以关闭；目标文件已 fsync，避免队列读到短文件
func copyFdToTemp(dir, src string) (string, int64, error) {
	in, err := os.Open(src)
	if err != nil {
		return "", 0, err
	}
	defer in.Close()
	out, err := os.CreateTemp(dir, ".mytgfs-dav-fd-*")
	if err != nil {
		return "", 0, err
	}
	name := out.Name()
	n, err := io.Copy(out, in)
	if err == nil {
		err = out.Sync()
	}
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(name)
		return "", 0, err
	}
	return name, n, nil
}

// ensureParents 建好目标文件的父目录层级。
//
// 输入：ctx；m 文件后端；target 卷内绝对文件路径
// 输出：error；父目录无法创建时返回
// 预期：
//   - 文件夹上传带相对路径时，层级要原样保留
//   - 已存在的目录视为成功（重复上传同一个文件夹不报错）
func ensureParents(ctx context.Context, m fm.Files, target string) error {
	dir := path.Dir(target)
	if dir == "/" || dir == "." {
		return nil
	}
	var cur strings.Builder
	for seg := range strings.SplitSeq(strings.TrimPrefix(dir, "/"), "/") {
		cur.WriteString("/" + seg)
		err := m.Mkdir(ctx, cur.String())
		if err == nil || errors.Is(err, fm.ErrExists) || errors.Is(err, os.ErrExist) {
			continue
		}
		return err
	}
	return nil
}

// handleAbort 放弃会话（DELETE /api/upload?id=）。
func (u *uploadAPI) handleAbort(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	u.drop(r.URL.Query().Get("id"))
	w.WriteHeader(http.StatusNoContent)
}

// handleStatus 返回会话进度（GET /api/upload?id=），供断线后接着传。
func (u *uploadAPI) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	s, ok := u.get(r.URL.Query().Get("id"))
	if !ok {
		replyJSONError(w, http.StatusNotFound, i18n.T("err.upload_no_session"))
		return
	}
	s.mu.Lock()
	done := s.done
	s.mu.Unlock()
	replyJSON(w, http.StatusOK, map[string]any{"offset": done, "path": path.Base(s.target)})
}

// get 取会话并刷新活跃时间。
//
// 输入：id 会话标识
// 输出：会话指针；false 表示不存在
// 预期：顺带回收闲置/超量会话（keep=id，本次要用的那个不会被回收）
func (u *uploadAPI) get(id string) (*uploadSession, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.evictLocked(id)
	s, ok := u.sess[id]
	if !ok {
		return nil, false
	}
	s.lastUsed.Store(time.Now().UnixNano())
	return s, true
}

// drop 关临时文件、删会话。
func (u *uploadAPI) drop(id string) {
	u.mu.Lock()
	s, ok := u.sess[id]
	if ok {
		delete(u.sess, id)
	}
	u.mu.Unlock()
	if !ok {
		return
	}
	s.mu.Lock()
	name := s.file.Name()
	_ = s.file.Close()
	adopted := s.adopted
	s.mu.Unlock()
	if adopted {
		// 文件已经交给上传队列，删掉会把重放要用的内容弄丢
		return
	}
	if err := os.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Warn(i18n.T("log.tmp_sweep_failed"), "path", name, "err", err)
	}
}

// evictLocked 回收超时或超量的会话；调用方需持 u.mu。
//
// 输入：keep 本次请求正在使用的会话 id，永不回收（传空表示没有）
// 输出：无
// 预期：
//   - 回收由每次会话请求触发，而不是只在新建会话时扫一遍，
//     否则用户放弃上传后再没有新上传，临时文件就一直留着
//   - 正在提交（busy）或正在写分片（TryLock 失败）的会话跳过：
//     关掉正在使用的句柄会把临时文件写坏，留到下一轮回收
func (u *uploadAPI) evictLocked(keep string) {
	cutoff := time.Now().Add(-uploadIdleTimeout).UnixNano()
	for id, s := range u.sess {
		if id == keep {
			continue
		}
		if s.lastUsed.Load() < cutoff && u.discard(s) {
			delete(u.sess, id)
		}
	}
	for len(u.sess) >= uploadMaxSessions {
		var oldest *uploadSession
		for id, s := range u.sess {
			if id == keep {
				continue
			}
			if !s.mu.TryLock() {
				continue
			}
			inUse := s.busy
			s.mu.Unlock()
			if inUse {
				continue
			}
			if oldest == nil || s.lastUsed.Load() < oldest.lastUsed.Load() {
				oldest = s
			}
		}
		if oldest == nil || !u.discard(oldest) {
			return
		}
		delete(u.sess, oldest.id)
	}
}

// discard 关闭并删除会话的临时文件。
//
// 输入：s 会话
// 输出：true 表示已关闭并删除；false 表示正在使用，本轮跳过
func (u *uploadAPI) discard(s *uploadSession) bool {
	if !s.mu.TryLock() {
		return false
	}
	if s.busy {
		s.mu.Unlock()
		return false
	}
	name := s.file.Name()
	_ = s.file.Close()
	s.mu.Unlock()
	_ = os.Remove(name)
	return true
}

// newToken 生成 n 字节随机数的十六进制串。
func newToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// 随机源不可用时退回时间戳，仍然保证唯一性足够本次会话使用
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(b)
}
