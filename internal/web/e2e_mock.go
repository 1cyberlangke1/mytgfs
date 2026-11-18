//go:build e2e

package web

// 桌面 e2e 用的假数据层：不连 Telegram、不读账号，给面板一份确定的卷与目录树。
//
// 只在 `-tags e2e` 的壳里挂载（见 trayapp 的 e2eWrap），正式二进制不含这段代码。
// 目的是让「进入目录 / 改名 / 删除确认 / 下载页」这些键盘流程能在 CI 上跑。

import (
	"encoding/json"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"mytgfs/internal/fm"
)

// mockVolume 是假卷名；面板侧栏与文件接口都用它。
const mockVolume = "demo"

// mockJob 是一条假队列任务：初始排队，取消接口会把它落成 cancelled。
//
// 字段与 queue.Item 的 JSON 形状一致，面板按同一套键渲染。
type mockJob struct {
	ID      int64  `json:"id"`
	Volume  string `json:"volume"`
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	State   string `json:"state"`
	Attempt int    `json:"attempt"`
	Done    int64  `json:"done,omitempty"`
	Size    int64  `json:"size,omitempty"`
}

// mockQueue 是假队列的唯一来源；e2e 单进程，用互斥锁串行化读写。
var mockQueue = struct {
	sync.Mutex
	jobs []mockJob
}{jobs: []mockJob{{
	ID: 1, Volume: mockVolume, Kind: "upload", Name: "queued-demo.bin", State: "queued", Size: 4096,
}}}

// mockQueueSnapshot 复制一份队列，避免调用方持锁渲染。
//
// 输入：无
// 输出：当前假队列的副本
// 预期：/api/state 每次返回同一批任务，取消后状态跟着变
func mockQueueSnapshot() []mockJob {
	mockQueue.Lock()
	defer mockQueue.Unlock()
	out := make([]mockJob, len(mockQueue.jobs))
	copy(out, mockQueue.jobs)
	return out
}

// mockCancelJob 处理取消请求。
//
// 输入：任务号
// 输出：HTTP 状态码与是否命中
// 预期：排队中/执行中改为 cancelled；不存在 404；已结束 409
func mockCancelJob(id int64) int {
	mockQueue.Lock()
	defer mockQueue.Unlock()
	for i := range mockQueue.jobs {
		job := &mockQueue.jobs[i]
		if job.ID != id {
			continue
		}
		switch job.State {
		case "queued", "running":
			job.State = "cancelled"
			return http.StatusOK
		default:
			return http.StatusConflict
		}
	}
	return http.StatusNotFound
}

// mockEmpty 报告是否用「空卷」跑 e2e：state 报 0 条、/api/files 一律返回空树，
// 用于复现空卷下的界面行为（骨架屏不应在刷新时重放）。
func mockEmpty() bool {
	return os.Getenv("MYTGFS_E2E_EMPTY") == "1"
}

// mockEntry 是假文件树里的一个节点。
type mockEntry struct {
	Path string
	Dir  bool
	Size int64
}

// mockState 是假文件树的全局状态；e2e 单进程，用互斥锁串行化写操作。
var mockState = struct {
	sync.Mutex
	entries map[string]mockEntry
}{entries: seedMock()}

// seedMock 生成固定的两目录 + 若干文件，覆盖目录、文本、图片、音视频与压缩包。
//
// 输入：无
// 输出：路径到节点的映射（根目录 "/" 不存）
// 预期：目录项都带对应子文件，便于测试逐层进出
func seedMock() map[string]mockEntry {
	m := map[string]mockEntry{}
	add := func(p string, dir bool, size int64) {
		m[p] = mockEntry{Path: p, Dir: dir, Size: size}
	}
	add("/photos", true, 0)
	add("/photos/cat.jpg", false, 118*1024)
	add("/photos/dog.png", false, 96*1024)
	add("/music", true, 0)
	add("/music/song.mp3", false, 2048*1024)
	add("/notes.txt", false, 1024)
	add("/report.pdf", false, 64*1024)
	add("/archive.zip", false, 512*1024)
	add("/clip.mp4", false, 1024*1024)
	return m
}

// E2EMock 在真面板前面挂一层假 API。
//
// 输入：next 真面板 handler
// 输出：先命中假数据、其余透传的 handler
// 预期：只接管 /api 下与文件/状态相关的只读与写接口，静态资源与 SSE 仍走真实现
func E2EMock(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if mockRoute(w, r) {
			return
		}
		next.ServeHTTP(w, r)
	})
}

// mockRoute 处理一个假接口。
//
// 输入：响应器与请求
// 输出：true 表示已处理
// 预期：未匹配的路径返回 false，交给真面板
func mockRoute(w http.ResponseWriter, r *http.Request) bool {
	// 单条任务取消：DELETE /api/queue/{vol}/{id}
	if strings.HasPrefix(r.URL.Path, "/api/queue/") {
		if r.Method != http.MethodDelete {
			replyJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
			return true
		}
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/queue/"), "/")
		if len(parts) != 2 || parts[0] != mockVolume {
			replyJSONError(w, http.StatusNotFound, "job not found")
			return true
		}
		id, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil || id <= 0 {
			replyJSONError(w, http.StatusBadRequest, "bad id")
			return true
		}
		switch code := mockCancelJob(id); code {
		case http.StatusOK:
			replyJSON(w, code, map[string]any{"cancelled": true})
		case http.StatusConflict:
			replyJSONError(w, code, "job finished")
		default:
			replyJSONError(w, code, "job not found")
		}
		return true
	}
	switch r.URL.Path {
	case "/api/state":
		if r.Method != http.MethodGet {
			replyJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
			return true
		}
		mockState.Lock()
		var files, used int64
		for _, e := range mockState.entries {
			if e.Dir {
				continue
			}
			files++
			used += e.Size
		}
		count := files
		mockState.Unlock()
		if mockEmpty() {
			count, used = 0, 0
		}
		queue := mockQueueSnapshot()
		if mockEmpty() {
			queue = nil
		}
		replyJSON(w, http.StatusOK, map[string]any{
			"volumes": []volumeState{{
				ID:      mockVolume,
				Name:    mockVolume,
				Title:   "演示卷",
				Entries: count,
				Used:    used,
			}},
			"caps": map[string]bool{
				"createVolume": false,
				"deleteVolume": false,
				"attachVolume": false,
				"listChannels": false,
				"login":        false,
				"autostart":    false,
				"cover":        false,
				"rebuild":      false,
			},
			"status": map[string]any{"state_code": "online", "queue": queue},
		})
		return true
	case "/api/queue":
		if r.Method != http.MethodDelete {
			replyJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
			return true
		}
		var kept []mockJob
		var removed int
		mockQueue.Lock()
		for _, job := range mockQueue.jobs {
			if job.State == "done" || job.State == "failed" || job.State == "cancelled" {
				removed++
				continue
			}
			kept = append(kept, job)
		}
		mockQueue.jobs = kept
		mockQueue.Unlock()
		replyJSON(w, http.StatusOK, map[string]any{"removed": removed})
		return true
	case "/api/files":
		if r.Method != http.MethodGet {
			replyJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
			return true
		}
		q := r.URL.Query()
		items := mockList(q.Get("dir"), q.Get("all") == "1")
		if mockEmpty() {
			items = nil
		}
		replyJSON(w, http.StatusOK, map[string]any{
			"files": items,
			"dir":   normalizeDir(q.Get("dir")),
		})
		return true
	case "/api/files/mkdir":
		var body jsonBody
		if !decodeMockBody(w, r, &body) {
			return true
		}
		target := path.Join(normalizeDir(body.Dir), strings.TrimSpace(body.Name))
		mockState.Lock()
		mockState.entries[target] = mockEntry{Path: target, Dir: true}
		mockState.Unlock()
		replyJSON(w, http.StatusOK, map[string]any{"path": target})
		return true
	case "/api/files/remove":
		var body jsonBody
		if !decodeMockBody(w, r, &body) {
			return true
		}
		mockState.Lock()
		for _, p := range body.Paths {
			mockRemoveLocked(p)
		}
		mockState.Unlock()
		replyJSON(w, http.StatusOK, map[string]any{"removed": len(body.Paths)})
		return true
	case "/api/files/move":
		var body jsonBody
		if !decodeMockBody(w, r, &body) {
			return true
		}
		mockState.Lock()
		mockMoveLocked(body.Paths, body.To, body.Dest)
		mockState.Unlock()
		replyJSON(w, http.StatusOK, map[string]any{"moved": len(body.Paths)})
		return true
	case "/api/file":
		// 预览只给一小段占位内容：e2e 验证的是键盘流程，不校验真实字节
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("mock"))
		return true
	case "/api/downloads":
		if r.Method != http.MethodGet {
			replyJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
			return true
		}
		now := time.Now().UTC().Format(time.RFC3339)
		replyJSON(w, http.StatusOK, map[string]any{
			"dir": filepath.Join(os.TempDir(), "mock", "downloads"),
			"files": []map[string]any{
				{"name": "已下载报告.pdf", "size": 65536, "mod": now},
				{"name": "已下载歌曲.mp3", "size": 2048 * 1024, "mod": now},
			},
			"tasks": []any{},
		})
		return true
	}
	return false
}

// mockList 返回某个目录的条目，all 为真时返回整棵树。
//
// 输入：dir 查询参数，空表示根；all 是否递归
// 输出：fileItem 切片；all 为真时包含所有后代
// 预期：根目录的 parentId 为 null，子项目 parentId 为所在目录
func mockList(dir string, all bool) []fileItem {
	base := normalizeDir(dir)
	out := make([]fileItem, 0, 8)
	mockState.Lock()
	defer mockState.Unlock()
	for p, e := range mockState.entries {
		if !all && path.Dir(p) != base {
			continue
		}
		if all && base != "/" && p != base && !strings.HasPrefix(p, base+"/") {
			continue
		}
		out = append(out, toFileItem(mockVolume, p, fm.Entry{
			Name: path.Base(p),
			Dir:  e.Dir,
			Size: e.Size,
			Mod:  time.Now(),
		}))
	}
	return out
}

// mockRemoveLocked 删除一个节点及其子树。
//
// 输入：p 绝对路径（调用方已持锁）
// 输出：无
// 预期：目录前缀匹配的所有后代一起删，和真实现的递归删除一致
func mockRemoveLocked(p string) {
	p = normalizeDir(p)
	for key := range mockState.entries {
		if key == p || strings.HasPrefix(key, p+"/") {
			delete(mockState.entries, key)
		}
	}
}

// mockMoveLocked 把一批节点移到目标目录或改名。
//
// 输入：paths 源路径；to 目标目录；dest 单条改名时的完整目标（调用方已持锁）
// 输出：无
// 预期：dest 非空按单条改名处理；否则逐条搬到 to 下沿用原名
func mockMoveLocked(paths []string, to, dest string) {
	if dest != "" && len(paths) == 1 {
		src := normalizeDir(paths[0])
		mockRenameLocked(src, normalizeDir(dest))
		return
	}
	dir := normalizeDir(to)
	for _, p := range paths {
		src := normalizeDir(p)
		mockRenameLocked(src, path.Join(dir, path.Base(src)))
	}
}

// mockRenameLocked 移动单个节点，子树路径一起改写。
//
// 输入：src 源路径；dst 目标路径（调用方已持锁）
// 输出：无
// 预期：目标已存在时覆盖；子树按前缀替换
func mockRenameLocked(src, dst string) {
	type kv struct {
		from, to string
		entry    mockEntry
	}
	var moves []kv
	for key, e := range mockState.entries {
		if key == src {
			moves = append(moves, kv{key, dst, e})
			continue
		}
		if strings.HasPrefix(key, src+"/") {
			moves = append(moves, kv{key, dst + strings.TrimPrefix(key, src), e})
		}
	}
	for _, m := range moves {
		delete(mockState.entries, m.from)
		m.entry.Path = m.to
		mockState.entries[m.to] = m.entry
	}
}

// decodeMockBody 解析写操作入参；失败时已写 400。
//
// 输入：响应器与请求；目标结构指针
// 输出：是否解析成功
// 预期：坏 JSON 返回 400，调用方直接返回
func decodeMockBody(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		replyJSONError(w, http.StatusBadRequest, "bad json")
		return false
	}
	return true
}
