package web

// 流式读取的窗口契约：/api/file/window 只认 offset/length，200 + 原始字节。

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"mytgfs/internal/fm"
	"mytgfs/internal/vfs"
)

// windowFiles 是带可读文件的假后端：Open 返回内存文件，Info 返回长度。
//
// 输入：data 文件内容
// 输出：实现 fm.Files 的最小集合
// 预期：只有 /clip.mp4 可读，其余路径一律 os.ErrNotExist
type windowFiles struct {
	*fakeFiles
	data []byte
}

func newWindowFiles(data []byte) *windowFiles {
	return &windowFiles{fakeFiles: newFakeFiles(), data: data}
}

func (w *windowFiles) Open(ctx context.Context, p string) (vfs.File, error) {
	if p != "/clip.mp4" {
		return nil, fs.ErrNotExist
	}
	return &windowFile{name: p, r: bytes.NewReader(w.data), size: int64(len(w.data))}, nil
}

func (w *windowFiles) Info(ctx context.Context, p string) (fm.Entry, error) {
	if p != "/clip.mp4" {
		return fm.Entry{}, fs.ErrNotExist
	}
	return fm.Entry{Name: p, Size: int64(len(w.data))}, nil
}

// windowFile 是只读内存文件，Seek 行为与 os.File 一致。
type windowFile struct {
	name string
	r    *bytes.Reader
	size int64
}

func (f *windowFile) Read(p []byte) (int, error)                { return f.r.Read(p) }
func (f *windowFile) Write([]byte) (int, error)                 { return 0, fs.ErrPermission }
func (f *windowFile) Close() error                              { return nil }
func (f *windowFile) Seek(off int64, whence int) (int64, error) { return f.r.Seek(off, whence) }

func (f *windowFile) Readdir(int) ([]fs.FileInfo, error) { return nil, fs.ErrInvalid }
func (f *windowFile) Stat() (fs.FileInfo, error)         { return windowInfo{f}, nil }

// windowInfo 是 windowFile 的 FileInfo 视图。
type windowInfo struct{ f *windowFile }

func (i windowInfo) Name() string       { return i.f.name }
func (i windowInfo) Size() int64        { return i.f.size }
func (i windowInfo) Mode() fs.FileMode  { return 0o444 }
func (i windowInfo) ModTime() time.Time { return time.Unix(0, 0) }
func (i windowInfo) IsDir() bool        { return false }
func (i windowInfo) Sys() any           { return nil }

// newWindowAPI 构造只认 demo 卷的 filesAPI。
func newWindowAPI(data []byte) *filesAPI {
	files := newWindowFiles(data)
	return &filesAPI{manager: func(volume string) (fm.Files, bool) {
		if volume != "demo" {
			return nil, false
		}
		return files, true
	}}
}

func TestRangeWindow(t *testing.T) {
	cases := []struct {
		name   string
		header string
		size   int64
		start  int64
		end    int64
	}{
		{name: "无 Range 从头开始", header: "", size: 100, start: 0, end: 99},
		{name: "开区间到文件尾", header: "bytes=0-", size: 100, start: 0, end: 99},
		{name: "中间闭区间", header: "bytes=10-19", size: 100, start: 10, end: 19},
		{name: "尾部后缀", header: "bytes=-20", size: 100, start: 80, end: 99},
		{name: "结束点越界夹到文件尾", header: "bytes=90-999", size: 100, start: 90, end: 99},
		{name: "起点越界返回空区间", header: "bytes=150-", size: 100, start: 100, end: 99},
		{name: "空文件", header: "", size: 0, start: 0, end: 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			start, end := rangeWindow(c.header, c.size)
			if start != c.start || end != c.end {
				t.Fatalf("rangeWindow(%q, %d) = %d..%d, want %d..%d",
					c.header, c.size, start, end, c.start, c.end)
			}
		})
	}
}

func TestFileWindowReads(t *testing.T) {
	api := newWindowAPI([]byte("hello world"))
	h := handleAPIFileWindow(api)

	cases := []struct {
		name   string
		query  string
		status int
		body   string
	}{
		{name: "开头窗口", query: "?vol=demo&path=/clip.mp4&offset=0&length=5", status: 200, body: "hello"},
		{name: "跨字节窗口", query: "?vol=demo&path=/clip.mp4&offset=6&length=5", status: 200, body: "world"},
		{name: "请求长度超剩余即短读", query: "?vol=demo&path=/clip.mp4&offset=8&length=8", status: 200, body: "rld"},
		{name: "偏移等于长度即 502", query: "?vol=demo&path=/clip.mp4&offset=11&length=1", status: 502},
		{name: "长度为零", query: "?vol=demo&path=/clip.mp4&offset=0&length=0", status: 400},
		{name: "负偏移", query: "?vol=demo&path=/clip.mp4&offset=-1&length=4", status: 400},
		{name: "长度超上限", query: "?vol=demo&path=/clip.mp4&offset=0&length=8388609", status: 400},
		{name: "未知卷", query: "?vol=none&path=/clip.mp4&offset=0&length=4", status: 404},
		{name: "路径带父目录", query: "?vol=demo&path=/../x&offset=0&length=4", status: 400},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h(rec, httptest.NewRequest(http.MethodGet, "/api/file/window"+c.query, nil))
			if rec.Code != c.status {
				t.Fatalf("status = %d, want %d (body=%q)", rec.Code, c.status, rec.Body.String())
			}
			if c.status == 200 && rec.Body.String() != c.body {
				t.Fatalf("body = %q, want %q", rec.Body.String(), c.body)
			}
			if c.status == 200 && rec.Header().Get("Content-Type") != "application/octet-stream" {
				t.Fatalf("content-type = %q", rec.Header().Get("Content-Type"))
			}
		})
	}
}

func TestFileWindowHead(t *testing.T) {
	api := newWindowAPI([]byte("hello world"))
	rec := httptest.NewRecorder()
	handleAPIFileWindow(api)(rec, httptest.NewRequest(
		http.MethodHead, "/api/file/window?vol=demo&path=/clip.mp4&offset=0&length=5", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("HEAD 不应该带正文，实际 %q", rec.Body.String())
	}
	if rec.Header().Get("Content-Length") != "5" {
		t.Fatalf("content-length = %q", rec.Header().Get("Content-Length"))
	}
}

func TestFileSize(t *testing.T) {
	api := newWindowAPI([]byte("hello world"))
	h := handleAPIFileSize(api)

	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/api/file/size?vol=demo&path=/clip.mp4", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var got struct {
		Size int64 `json:"size"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Size != int64(len("hello world")) {
		t.Fatalf("size = %d", got.Size)
	}

	rec = httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/api/file/size?vol=demo&path=/missing.mp4", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing status = %d", rec.Code)
	}
}
