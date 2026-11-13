package web

// 文件管理 handler 单测：用假后端验证列出、上传、新建、移动、删除、下载。

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/webdav"

	"mytgfs/internal/fm"
	"mytgfs/internal/i18n"
)

// fakeFiles 是 fm.Files 的假实现。
type fakeFiles struct {
	entries map[string][]fm.Entry
	content map[string][]byte
	uploads map[string]string
	mkdirs  []string
	removed []string
	moves   [][2]string
	listErr error
}

func newFakeFiles() *fakeFiles {
	return &fakeFiles{
		entries: map[string][]fm.Entry{},
		content: map[string][]byte{},
		uploads: map[string]string{},
	}
}

func (f *fakeFiles) List(_ context.Context, dir string) ([]fm.Entry, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.entries[dir], nil
}

func (f *fakeFiles) Info(_ context.Context, p string) (fm.Entry, error) {
	for _, list := range f.entries {
		for _, e := range list {
			if e.Name == path.Base(p) {
				return e, nil
			}
		}
	}
	return fm.Entry{}, os.ErrNotExist
}

func (f *fakeFiles) Mkdir(_ context.Context, p string) error {
	if p == "/exists" {
		return fm.ErrExists
	}
	f.mkdirs = append(f.mkdirs, p)
	return nil
}

func (f *fakeFiles) Upload(_ context.Context, p string, r io.Reader) error {
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	f.uploads[p] = string(b)
	return nil
}

func (f *fakeFiles) Remove(_ context.Context, p string) error {
	if p == "/bad" {
		return fm.ErrBadPath
	}
	f.removed = append(f.removed, p)
	return nil
}

func (f *fakeFiles) Move(_ context.Context, from, to string) error {
	if to == "/taken" {
		return fm.ErrExists
	}
	f.moves = append(f.moves, [2]string{from, to})
	return nil
}

func (f *fakeFiles) Open(_ context.Context, p string) (webdav.File, error) {
	data, ok := f.content[p]
	if !ok {
		return nil, os.ErrNotExist
	}
	return &memFile{name: path.Base(p), data: data}, nil
}

// memFile 是只读的 webdav.File 假实现，供下载测试使用。
type memFile struct {
	name string
	data []byte
	off  int
}

func (m *memFile) Read(p []byte) (int, error) {
	if m.off >= len(m.data) {
		return 0, io.EOF
	}
	n := copy(p, m.data[m.off:])
	m.off += n
	return n, nil
}

func (m *memFile) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
		m.off = int(offset)
	case io.SeekCurrent:
		m.off += int(offset)
	case io.SeekEnd:
		m.off = len(m.data) + int(offset)
	}
	return int64(m.off), nil
}

func (m *memFile) Readdir(int) ([]fs.FileInfo, error) { return nil, nil }
func (m *memFile) Stat() (fs.FileInfo, error)         { return memInfo{m}, nil }
func (m *memFile) Close() error                       { return nil }

// Write 只是为了让测试文件满足 webdav.File 接口，下载路径不会调用它。
func (m *memFile) Write([]byte) (int, error) { return 0, errors.New("read-only") }

type memInfo struct{ f *memFile }

func (i memInfo) Name() string       { return i.f.name }
func (i memInfo) Size() int64        { return int64(len(i.f.data)) }
func (i memInfo) Mode() fs.FileMode  { return 0o644 }
func (i memInfo) ModTime() time.Time { return time.Unix(0, 0) }
func (i memInfo) IsDir() bool        { return false }
func (i memInfo) Sys() any           { return nil }

// newTestAPI 组装一个只有一个卷的 filesAPI。
func newTestAPI(f *fakeFiles) *filesAPI {
	return newFilesAPI(
		func(string) (fm.Files, bool) { return f, true },
		func() []string { return []string{"demo"} },
		"",
	)
}

func TestNewFilesAPINilWhenIncomplete(t *testing.T) {
	if newFilesAPI(nil, func() []string { return nil }, "") != nil {
		t.Error("缺 manager 时应退化成 nil")
	}
	if newFilesAPI(func(string) (fm.Files, bool) { return nil, false }, nil, "") != nil {
		t.Error("缺 volumes 时应退化成 nil")
	}
}

// TestFragFilesLists 列表要包含目录链接、文件下载链接与大小。
func TestFragFilesLists(t *testing.T) {
	f := newFakeFiles()
	f.entries["/"] = []fm.Entry{
		{Name: "movies", Dir: true},
		{Name: "a.bin", Size: 2048, Mod: time.Unix(0, 0)},
	}
	rec := httptest.NewRecorder()
	handleFragFiles(newTestAPI(f))(rec, httptest.NewRequest(http.MethodGet, "/frag/files?vol=demo&dir=/", nil))
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d", rec.Code)
	}
	if !strings.Contains(body, `dir=%2Fmovies`) {
		t.Errorf("目录缺少跳转链接: %s", body)
	}
	if !strings.Contains(body, `/files/download?`) || !strings.Contains(body, `%2Fa.bin`) {
		if len(body) > 700 {
			body = body[len(body)-700:]
		}
		t.Errorf("文件缺少下载链接（末尾片段）: %s", body)
	}
	if !strings.Contains(body, "2.0 KB") {
		t.Errorf("大小未格式化: %s", body)
	}
}

// TestFragFilesUnavailable 没有文件管理能力时返回 501。
func TestFragFilesUnavailable(t *testing.T) {
	rec := httptest.NewRecorder()
	handleFragFiles(nil)(rec, httptest.NewRequest(http.MethodGet, "/frag/files", nil))
	if rec.Code != http.StatusNotImplemented {
		t.Errorf("code = %d", rec.Code)
	}
}

// TestFragFilesListError 列目录失败也要返回 200 + 错误文案。
func TestFragFilesListError(t *testing.T) {
	f := newFakeFiles()
	f.listErr = errors.New("boom")
	rec := httptest.NewRecorder()
	handleFragFiles(newTestAPI(f))(rec, httptest.NewRequest(http.MethodGet, "/frag/files?vol=demo", nil))
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, "boom") {
		t.Fatalf("code = %d body = %s", rec.Code, body)
	}
}

// TestFragFilesMkdirEmptyName 空名字不调用后端。
func TestFragFilesMkdirEmptyName(t *testing.T) {
	f := newFakeFiles()
	req := formRequest("/frag/files/mkdir", "vol=demo&dir=/&name=+")
	rec := httptest.NewRecorder()
	handleFragFilesMkdir(newTestAPI(f))(rec, req)
	if len(f.mkdirs) != 0 {
		t.Errorf("不应建目录: %v", f.mkdirs)
	}
	if !strings.Contains(rec.Body.String(), `class="err"`) {
		t.Errorf("body = %s", rec.Body.String())
	}
}

// TestFragFilesMkdirRejectsSlash 名字里带 / 一律拒绝。
func TestFragFilesMkdirRejectsSlash(t *testing.T) {
	f := newFakeFiles()
	req := formRequest("/frag/files/mkdir", "vol=demo&dir=/&name=evil%2Fpath")
	rec := httptest.NewRecorder()
	handleFragFilesMkdir(newTestAPI(f))(rec, req)
	if len(f.mkdirs) != 0 {
		t.Errorf("应拒绝带斜杠的名字: %v", f.mkdirs)
	}
}

// TestFragFilesMkdirSuccess 成功后带回成功文案。
func TestFragFilesMkdirSuccess(t *testing.T) {
	f := newFakeFiles()
	req := formRequest("/frag/files/mkdir", "vol=demo&dir=/sub&name=docs")
	rec := httptest.NewRecorder()
	handleFragFilesMkdir(newTestAPI(f))(rec, req)
	if len(f.mkdirs) != 1 || f.mkdirs[0] != "/sub/docs" {
		t.Fatalf("mkdirs = %v", f.mkdirs)
	}
	if !strings.Contains(rec.Body.String(), `class="ok"`) {
		t.Errorf("body = %s", rec.Body.String())
	}
}

// TestFragFilesRemove 删除走路径参数，卷根由后端拒绝。
func TestFragFilesRemove(t *testing.T) {
	f := newFakeFiles()
	req := formRequest("/frag/files/remove", "vol=demo&dir=/&path=%2Fsub%2Fa.txt")
	rec := httptest.NewRecorder()
	handleFragFilesRemove(newTestAPI(f))(rec, req)
	if len(f.removed) != 1 || f.removed[0] != "/sub/a.txt" {
		t.Fatalf("removed = %v", f.removed)
	}

	// 后端报错（如卷根）要显示错误样式
	req = formRequest("/frag/files/remove", "vol=demo&dir=/&path=%2Fbad")
	rec = httptest.NewRecorder()
	handleFragFilesRemove(newTestAPI(f))(rec, req)
	if !strings.Contains(rec.Body.String(), `class="err"`) {
		t.Errorf("应显示错误: %s", rec.Body.String())
	}
}

// TestFragFilesMove 相对目标按当前目录拼接。
func TestFragFilesMove(t *testing.T) {
	f := newFakeFiles()
	req := formRequest("/frag/files/move", "vol=demo&dir=%2Fdocs&from=%2Fdocs%2Fa.txt&to=renamed.txt")
	rec := httptest.NewRecorder()
	handleFragFilesMove(newTestAPI(f))(rec, req)
	if len(f.moves) != 1 || f.moves[0] != [2]string{"/docs/a.txt", "/docs/renamed.txt"} {
		t.Fatalf("moves = %v", f.moves)
	}

	// 绝对路径原样透传
	req = formRequest("/frag/files/move", "vol=demo&dir=%2F&from=%2Fa.txt&to=%2Fsub%2Fa.txt")
	rec = httptest.NewRecorder()
	handleFragFilesMove(newTestAPI(f))(rec, req)
	if f.moves[1] != [2]string{"/a.txt", "/sub/a.txt"} {
		t.Fatalf("moves = %v", f.moves)
	}
}

// TestFragFilesMoveEmptyTarget 空目标直接拒绝。
func TestFragFilesMoveEmptyTarget(t *testing.T) {
	f := newFakeFiles()
	req := formRequest("/frag/files/move", "vol=demo&dir=%2F&from=%2Fa.txt&to=+")
	rec := httptest.NewRecorder()
	handleFragFilesMove(newTestAPI(f))(rec, req)
	if len(f.moves) != 0 {
		t.Errorf("不应调用移动: %v", f.moves)
	}
	if !strings.Contains(rec.Body.String(), `class="err"`) {
		t.Errorf("body = %s", rec.Body.String())
	}
}

// TestFragFilesUpload 多文件上传，全部落库并给出数量反馈。
func TestFragFilesUpload(t *testing.T) {
	f := newFakeFiles()
	req := uploadRequest(t, map[string]string{"a.txt": "alpha", "b.txt": "bravo"})
	rec := httptest.NewRecorder()
	handleFragFilesUpload(newTestAPI(f))(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d", rec.Code)
	}
	if f.uploads["/a.txt"] != "alpha" || f.uploads["/b.txt"] != "bravo" {
		t.Fatalf("uploads = %v", f.uploads)
	}
	// 文案随语言变化，断言走 i18n 而不是硬编码中文
	if want := i18n.T("web.files_uploaded", 2); !strings.Contains(rec.Body.String(), want) {
		t.Errorf("body 缺少 %q: %s", want, rec.Body.String())
	}
}

// TestFragFilesUploadRejectsUnsafeName 带路径的文件名不能越出卷根。
//
// Go 的 multipart 会把 filename 取成 filepath.Base，所以 ../../etc/passwd
// 落到根目录的 passwd；这里断言它没有被写到根之外。
func TestFragFilesUploadRejectsUnsafeName(t *testing.T) {
	f := newFakeFiles()
	req := uploadRequest(t, map[string]string{"../../etc/passwd": "pwn"})
	rec := httptest.NewRecorder()
	handleFragFilesUpload(newTestAPI(f))(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("code = %d", rec.Code)
	}
	for p := range f.uploads {
		if strings.Contains(p, "..") || !strings.HasPrefix(p, "/") {
			t.Fatalf("路径越界: %q", p)
		}
	}
	if _, ok := f.uploads["/passwd"]; !ok {
		t.Fatalf("应落在卷根，实际 %v", f.uploads)
	}
}

// TestFileDownload 下载返回内容，并支持 Range。
func TestFileDownload(t *testing.T) {
	f := newFakeFiles()
	f.content["/x.bin"] = []byte("0123456789")
	rec := httptest.NewRecorder()
	handleFileDownload(newTestAPI(f))(rec, httptest.NewRequest(http.MethodGet, "/files/download?vol=demo&path=%2Fx.bin", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "0123456789" {
		t.Fatalf("code = %d body = %q", rec.Code, rec.Body.String())
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "attachment") {
		t.Errorf("Content-Disposition = %q", cd)
	}

	req := httptest.NewRequest(http.MethodGet, "/files/download?vol=demo&path=%2Fx.bin", nil)
	req.Header.Set("Range", "bytes=2-4")
	rec = httptest.NewRecorder()
	handleFileDownload(newTestAPI(f))(rec, req)
	if rec.Code != http.StatusPartialContent {
		t.Fatalf("Range 请求 code = %d", rec.Code)
	}
	if rec.Body.String() != "234" {
		t.Errorf("Range 内容 = %q", rec.Body.String())
	}
}

// TestFileDownloadMissing 不存在的文件返回 404。
func TestFileDownloadMissing(t *testing.T) {
	rec := httptest.NewRecorder()
	handleFileDownload(newTestAPI(newFakeFiles()))(rec,
		httptest.NewRequest(http.MethodGet, "/files/download?vol=demo&path=%2Fnope", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("code = %d", rec.Code)
	}
}

// TestCrumbsOf 面包屑按层级拆分。
func TestCrumbsOf(t *testing.T) {
	got := crumbsOf("/a/b")
	if len(got) != 3 || got[0].Path != "/" || got[1].Path != "/a" || got[2].Path != "/a/b" {
		t.Fatalf("crumbs = %+v", got)
	}
	if one := crumbsOf("/"); len(one) != 1 {
		t.Errorf("根目录 crumb = %+v", one)
	}
}

// TestNormalizeDir 目录参数规范化。
func TestNormalizeDir(t *testing.T) {
	cases := map[string]string{"": "/", "a": "/a", "/a/": "/a", `a\b`: "/a/b"}
	for in, want := range cases {
		if got := normalizeDir(in); got != want {
			t.Errorf("normalizeDir(%q) = %q，期望 %q", in, got, want)
		}
	}
}

// TestSafeName 名字校验。
func TestSafeName(t *testing.T) {
	for _, bad := range []string{"", " ", "..", ".", "a/b", `a\b`} {
		if got := safeName(bad); got != "" {
			t.Errorf("safeName(%q) = %q，期望空", bad, got)
		}
	}
	if got := safeName(" ok.txt "); got != "ok.txt" {
		t.Errorf("safeName 应去空白，实际 %q", got)
	}
}

// formRequest 构造表单 POST 请求。
func formRequest(_ string, body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/frag/files", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

// uploadRequest 构造 multipart 上传请求（目标目录 /）。
func uploadRequest(t *testing.T, files map[string]string) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if err := mw.WriteField("vol", "demo"); err != nil {
		t.Fatal(err)
	}
	if err := mw.WriteField("dir", "/"); err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		fw, err := mw.CreateFormFile("file", name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/frag/files/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}
