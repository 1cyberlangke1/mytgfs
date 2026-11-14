package web

// 下载目录单测：后台任务、断点续传、重名、删除与接口校验。

import (
	"bytes"
	"context"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mytgfs/internal/fm"
	"mytgfs/internal/vfs"
)

// openFiles 把 Open 指到真实临时目录，供下载任务读。
type openFiles struct {
	*fakeFiles
	root string
}

func (f openFiles) Open(ctx context.Context, p string) (vfs.File, error) {
	return &ctxFile{File: mustOpen(f.root, p), ctx: ctx}, nil
}

func mustOpen(root, p string) *os.File {
	f, err := os.Open(filepath.Join(root, filepath.Base(p)))
	if err != nil {
		panic(err)
	}
	return f
}

// ctxFile 让 Read 响应取消，模拟真实网络读被暂停打断。
type ctxFile struct {
	*os.File
	ctx  context.Context
	slow bool
}

func (f *ctxFile) Read(p []byte) (int, error) {
	if f.slow {
		select {
		case <-f.ctx.Done():
			return 0, f.ctx.Err()
		case <-time.After(time.Millisecond):
		}
	}
	return f.File.Read(p)
}

func (f *ctxFile) Write([]byte) (int, error) { return 0, fs.ErrInvalid }
func (f *ctxFile) Readdir(int) ([]fs.FileInfo, error) {
	return nil, fs.ErrInvalid
}

// newDownloadFixture 建一个含 a.txt 的下载目录。
func newDownloadFixture(t *testing.T) (*downloadStore, string) {
	t.Helper()
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	ff := newFakeFiles()
	dir := t.TempDir()
	dl := newDownloadStore(dir, func(string) (fm.Files, bool) { return openFiles{ff, src}, true }, nil)
	return dl, dir
}

// waitDownloaded 等任务全部结束且目标文件出现。
func waitDownloaded(t *testing.T, dl *downloadStore, name string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(dl.Tasks()) == 0 {
			if name == "" {
				return
			}
			if _, err := os.Stat(filepath.Join(dl.dir, name)); err == nil {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("下载未完成: tasks=%+v", dl.Tasks())
}

// TestDownloadStartAndList 启动任务后文件落盘，列表带大小与时间。
func TestDownloadStartAndList(t *testing.T) {
	dl, dir := newDownloadFixture(t)
	task, err := dl.Start("demo", "/a.txt")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if task.Name != "a.txt" || task.Size != 5 || task.State != downloadRunning {
		t.Fatalf("task = %+v", task)
	}
	waitDownloaded(t, dl, "a.txt")

	got, err := os.ReadFile(filepath.Join(dir, "a.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello" {
		t.Fatalf("content = %q", got)
	}
	list, err := dl.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Name != "a.txt" || list[0].Mod == "" {
		t.Fatalf("list = %+v", list)
	}
}

// TestDownloadDuplicateNames 同名文件加序号，不覆盖已有内容。
func TestDownloadDuplicateNames(t *testing.T) {
	dl, dir := newDownloadFixture(t)
	for range 2 {
		if _, err := dl.Start("demo", "/a.txt"); err != nil {
			t.Fatalf("Start: %v", err)
		}
		waitDownloaded(t, dl, "")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if len(names) != 2 || !strings.Contains(strings.Join(names, ","), "a (1).txt") {
		t.Fatalf("names = %v", names)
	}
}

// TestDownloadBadInput 相对路径、向上越界、非法文件名一律拒绝。
func TestDownloadBadInput(t *testing.T) {
	dl, _ := newDownloadFixture(t)
	if _, err := dl.Start("demo", "relative.txt"); err != errDownloadPath {
		t.Fatalf("relative => %v", err)
	}
	if _, err := dl.Start("demo", "/../etc/passwd"); err != errDownloadPath {
		t.Fatalf("dotdot => %v", err)
	}
	if err := dl.Remove("../a.txt"); err != errDownloadName {
		t.Fatalf("remove traversal => %v", err)
	}
	if err := dl.Remove(""); err != errDownloadName {
		t.Fatalf("remove empty => %v", err)
	}
}

// TestDownloadUnknownVolume 卷名不存在时给出可区分错误。
func TestDownloadUnknownVolume(t *testing.T) {
	dl := newDownloadStore(t.TempDir(), func(string) (fm.Files, bool) { return nil, false }, nil)
	if _, err := dl.Start("nope", "/a.txt"); err != errDownloadVolume {
		t.Fatalf("err = %v", err)
	}
}

// TestDownloadRemove 删除后列表为空。
func TestDownloadRemove(t *testing.T) {
	dl, _ := newDownloadFixture(t)
	if _, err := dl.Start("demo", "/a.txt"); err != nil {
		t.Fatal(err)
	}
	waitDownloaded(t, dl, "a.txt")
	if err := dl.Remove("a.txt"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	list, err := dl.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("list = %+v", list)
	}
}

// TestDownloadPauseResume 暂停后状态为 paused，继续能读到完整内容。
//
// 输入：1MB 慢速文件；读到一部分时暂停
// 输出：暂停态、继续后内容一致
// 预期：继续从断点续写，而不是从头覆盖
func TestDownloadPauseResume(t *testing.T) {
	src := t.TempDir()
	payload := bytes.Repeat([]byte("0123456789abcdef"), 64<<10) // 1 MiB
	if err := os.WriteFile(filepath.Join(src, "big.bin"), payload, 0o600); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	dl := newDownloadStore(dir, func(string) (fm.Files, bool) {
		return openFiles{&fakeFiles{}, src}, true
	}, nil)
	// slow 模式由 ctxFile 决定；这里换一个会注入 slow 的 opener
	dl.open = func(string) (fm.Files, bool) {
		return fmFunc(func(ctx context.Context, p string) (vfs.File, error) {
			return &ctxFile{File: mustOpen(src, p), ctx: ctx, slow: true}, nil
		}), true
	}

	task, err := dl.Start("demo", "/big.bin")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if tasks := dl.Tasks(); len(tasks) == 1 && tasks[0].Done > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := dl.Pause(task.ID); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	tasks := dl.Tasks()
	if len(tasks) != 1 || tasks[0].State != downloadPaused {
		t.Fatalf("暂停后 tasks = %+v", tasks)
	}
	if err := dl.Resume(task.ID); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	waitDownloaded(t, dl, "big.bin")
	got, err := os.ReadFile(filepath.Join(dir, "big.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("内容不一致: %d != %d", len(got), len(payload))
	}
}

// fmFunc 是只实现 Open 的文件后端，其余方法返回未实现错误。
type fmFunc func(context.Context, string) (vfs.File, error)

func (f fmFunc) Open(ctx context.Context, p string) (vfs.File, error) { return f(ctx, p) }
func (fmFunc) List(context.Context, string) ([]fm.Entry, error)       { return nil, fs.ErrInvalid }
func (fmFunc) Search(context.Context, string, string, int) ([]fm.SearchHit, error) {
	return nil, fs.ErrInvalid
}
func (fmFunc) Info(context.Context, string) (fm.Entry, error)  { return fm.Entry{}, fs.ErrInvalid }
func (fmFunc) Mkdir(context.Context, string) error             { return fs.ErrInvalid }
func (fmFunc) Upload(context.Context, string, io.Reader) error { return fs.ErrInvalid }
func (fmFunc) Remove(context.Context, string) error            { return fs.ErrInvalid }
func (fmFunc) Move(context.Context, string, string) error      { return fs.ErrInvalid }

// TestSafeDownloadName 各平台非法字符替换成下划线，空名回落。
func TestSafeDownloadName(t *testing.T) {
	cases := map[string]string{
		"a:b*c?.txt": "a_b_c_.txt",
		"  report  ": "report",
		"...":        "download",
		"":           "download",
	}
	for in, want := range cases {
		if got := safeDownloadName(in); got != want {
			t.Fatalf("safeDownloadName(%q)=%q want %q", in, got, want)
		}
	}
}

// TestDownloadsAPI GET 返回目录与任务，POST 立即 202，删除清空。
func TestDownloadsAPI(t *testing.T) {
	dl, dir := newDownloadFixture(t)
	h := handleAPIDownloads(dl)

	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/api/downloads", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"dir"`) {
		t.Fatalf("GET = %d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	body := strings.NewReader(`{"vol":"demo","path":"/a.txt"}`)
	h(rec, httptest.NewRequest(http.MethodPost, "/api/downloads", body))
	if rec.Code != http.StatusAccepted || !strings.Contains(rec.Body.String(), `"task"`) {
		t.Fatalf("POST = %d %s", rec.Code, rec.Body.String())
	}
	waitDownloaded(t, dl, "a.txt")

	rec = httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodDelete, "/api/downloads?name=a.txt", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE = %d %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "a.txt")); !os.IsNotExist(err) {
		t.Fatalf("文件应已删除: %v", err)
	}
}

// TestDownloadTaskAPI 暂停/继续校验参数与状态。
func TestDownloadTaskAPI(t *testing.T) {
	dl, _ := newDownloadFixture(t)
	h := handleAPIDownloadTask(dl)

	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodPost, "/api/downloads/task", strings.NewReader(`{"id":"nope","action":"pause"}`)))
	if rec.Code != http.StatusConflict {
		t.Fatalf("missing task = %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodPost, "/api/downloads/task", strings.NewReader(`{"id":"x","action":"explode"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad action = %d %s", rec.Code, rec.Body.String())
	}
}

// TestDownloadsAPIUnavailable 无卷后端时返回 501，不假装成功。
func TestDownloadsAPIUnavailable(t *testing.T) {
	dl := newDownloadStore(t.TempDir(), nil, nil)
	rec := httptest.NewRecorder()
	body := strings.NewReader(`{"vol":"demo","path":"/a.txt"}`)
	handleAPIDownloads(dl)(rec, httptest.NewRequest(http.MethodPost, "/api/downloads", body))
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("POST = %d %s", rec.Code, rec.Body.String())
	}
}

// TestDownloadsOpenMethod 打开目录只接受 POST；GET 不触发系统调用。
func TestDownloadsOpenMethod(t *testing.T) {
	dl := newDownloadStore(t.TempDir(), nil, nil)
	rec := httptest.NewRecorder()
	handleAPIDownloadsOpen(dl)(rec, httptest.NewRequest(http.MethodGet, "/api/downloads/open", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET = %d", rec.Code)
	}
}

// TestDownloadFileServesContent 下载页预览走本地文件接口。
//
// 输入：已落盘的 a.txt
// 输出：200 + 文件内容
// 预期：带 Range 时仍由 ServeContent 处理，视频/音频可直接拖动
func TestDownloadFileServesContent(t *testing.T) {
	dl, _ := newDownloadFixture(t)
	if _, err := dl.Start("demo", "/a.txt"); err != nil {
		t.Fatal(err)
	}
	waitDownloaded(t, dl, "a.txt")

	h := handleAPIDownloadFile(dl)
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/api/downloads/file?name=a.txt", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "hello" {
		t.Fatalf("GET = %d %q", rec.Code, rec.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/api/downloads/file?name=a.txt", nil)
	req.Header.Set("Range", "bytes=1-3")
	rec = httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusPartialContent || rec.Body.String() != "ell" {
		t.Fatalf("Range = %d %q", rec.Code, rec.Body.String())
	}
}

// TestDownloadFileRejectsTraversal 预览接口不能越出下载目录。
func TestDownloadFileRejectsTraversal(t *testing.T) {
	dl, _ := newDownloadFixture(t)
	h := handleAPIDownloadFile(dl)
	for _, name := range []string{"../a.txt", "..", "", "sub/a.txt"} {
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest(http.MethodGet, "/api/downloads/file?name="+name, nil))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("name %q => %d，期望 400", name, rec.Code)
		}
	}
}

// TestDownloadFileMissing 文件不存在时 404，而不是 502。
func TestDownloadFileMissing(t *testing.T) {
	dl, _ := newDownloadFixture(t)
	rec := httptest.NewRecorder()
	handleAPIDownloadFile(dl)(rec, httptest.NewRequest(http.MethodGet, "/api/downloads/file?name=gone.txt", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("code = %d，期望 404", rec.Code)
	}
}

// TestDownloadFileOpenMethod 系统打开文件只接受 POST。
func TestDownloadFileOpenMethod(t *testing.T) {
	dl, _ := newDownloadFixture(t)
	rec := httptest.NewRecorder()
	handleAPIDownloadOpenFile(dl)(rec, httptest.NewRequest(http.MethodGet, "/api/downloads/open-file?name=a.txt", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET = %d", rec.Code)
	}
}
