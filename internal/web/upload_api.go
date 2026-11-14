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
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"mytgfs/internal/fm"
	"mytgfs/internal/i18n"
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
	done     int64
	file     *os.File
	lastUsed time.Time
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
		size: req.Size, file: f, lastUsed: time.Now(),
	}
	u.mu.Lock()
	u.evictLocked()
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
	if offset != s.done {
		replyJSON(w, http.StatusConflict, map[string]any{
			"offset": s.done, "error": i18n.T("err.upload_offset"),
		})
		return
	}
	n, cerr := io.Copy(s.file, r.Body)
	if cerr != nil {
		replyJSONError(w, http.StatusInternalServerError, cerr.Error())
		return
	}
	s.done += n
	s.lastUsed = time.Now()
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
	done, size := s.done, s.size
	s.mu.Unlock()
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
	if _, err := s.file.Seek(0, io.SeekStart); err != nil {
		u.drop(s.id)
		replyJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// 文件夹上传会带上相对路径：先把缺失的父目录补出来，再落文件
	if err := ensureParents(r.Context(), m, s.target); err != nil {
		u.drop(s.id)
		replyJSONError(w, http.StatusBadRequest, fmError(err))
		return
	}
	if err := m.Upload(r.Context(), s.target, s.file); err != nil {
		u.drop(s.id)
		replyJSONError(w, http.StatusBadRequest, fmError(err))
		return
	}
	u.drop(s.id)
	replyJSON(w, http.StatusOK, map[string]any{"path": s.target})
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
	var cur string
	for seg := range strings.SplitSeq(strings.TrimPrefix(dir, "/"), "/") {
		cur += "/" + seg
		err := m.Mkdir(ctx, cur)
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
func (u *uploadAPI) get(id string) (*uploadSession, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	s, ok := u.sess[id]
	if !ok {
		return nil, false
	}
	s.lastUsed = time.Now()
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
	s.mu.Unlock()
	if err := os.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Warn(i18n.T("log.tmp_sweep_failed"), "path", name, "err", err)
	}
}

// evictLocked 回收超时或超量的会话；调用方需持 u.mu。
func (u *uploadAPI) evictLocked() {
	cutoff := time.Now().Add(-uploadIdleTimeout)
	for id, s := range u.sess {
		if s.lastUsed.Before(cutoff) {
			delete(u.sess, id)
			_ = s.file.Close()
			_ = os.Remove(s.file.Name())
		}
	}
	for len(u.sess) >= uploadMaxSessions {
		var oldest *uploadSession
		for _, s := range u.sess {
			if oldest == nil || s.lastUsed.Before(oldest.lastUsed) {
				oldest = s
			}
		}
		if oldest == nil {
			return
		}
		delete(u.sess, oldest.id)
		_ = oldest.file.Close()
		_ = os.Remove(oldest.file.Name())
	}
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
