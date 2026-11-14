package web

// 下载目录单测：用真实临时目录验证落盘、重名、删除与参数校验。

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mytgfs/internal/fm"
	"mytgfs/internal/vfs"
)

// openFiles 在 fakeFiles 基础上把 Open 指到真实临时目录。
//
// 输入：root 源文件目录；uploads 里登记的文件名
// 输出：可读的 vfs.File
// 预期：只用于读取，不参与上传断言
type openFiles struct {
	*fakeFiles
	root string
}

func (f openFiles) Open(_ context.Context, p string) (vfs.File, error) {
	return os.Open(filepath.Join(f.root, filepath.Base(p)))
}

func newDownloadFixture(t *testing.T) (*downloadStore, string) {
	t.Helper()
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	ff := newFakeFiles()
	dir := t.TempDir()
	dl := newDownloadStore(dir, func(string) (fm.Files, bool) { return openFiles{ff, src}, true })
	return dl, dir
}

// TestDownloadSaveAndList 写入后文件落盘、列表带大小与时间。
func TestDownloadSaveAndList(t *testing.T) {
	dl, dir := newDownloadFixture(t)
	item, err := dl.Save(context.Background(), "demo", "/a.txt")
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if item.Name != "a.txt" || item.Size != 5 || item.Mod == "" {
		t.Fatalf("item = %+v", item)
	}
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
	if len(list) != 1 || list[0].Name != "a.txt" {
		t.Fatalf("list = %+v", list)
	}
}

// TestDownloadDuplicateNames 同名文件加序号，不覆盖已有内容。
func TestDownloadDuplicateNames(t *testing.T) {
	dl, dir := newDownloadFixture(t)
	for range 2 {
		if _, err := dl.Save(context.Background(), "demo", "/a.txt"); err != nil {
			t.Fatalf("Save: %v", err)
		}
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
	ctx := context.Background()
	if _, err := dl.Save(ctx, "demo", "relative.txt"); err != errDownloadPath {
		t.Fatalf("relative => %v", err)
	}
	if _, err := dl.Save(ctx, "demo", "/../etc/passwd"); err != errDownloadPath {
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
	dir := t.TempDir()
	dl := newDownloadStore(dir, func(string) (fm.Files, bool) { return nil, false })
	if _, err := dl.Save(context.Background(), "nope", "/a.txt"); err != errDownloadVolume {
		t.Fatalf("err = %v", err)
	}
}

// TestDownloadRemove 删除后列表为空。
func TestDownloadRemove(t *testing.T) {
	dl, _ := newDownloadFixture(t)
	if _, err := dl.Save(context.Background(), "demo", "/a.txt"); err != nil {
		t.Fatal(err)
	}
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

// TestDownloadsAPI GET 返回目录与列表，POST 落盘，DELETE 删除。
func TestDownloadsAPI(t *testing.T) {
	dl, _ := newDownloadFixture(t)
	h := handleAPIDownloads(dl)

	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/api/downloads", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"dir"`) {
		t.Fatalf("GET = %d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	body := strings.NewReader(`{"vol":"demo","path":"/a.txt"}`)
	h(rec, httptest.NewRequest(http.MethodPost, "/api/downloads", body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST = %d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodDelete, "/api/downloads?name=a.txt", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE = %d %s", rec.Code, rec.Body.String())
	}
}

// TestDownloadsAPIUnavailable 无卷后端时写入返回 501，不假装成功。
func TestDownloadsAPIUnavailable(t *testing.T) {
	dl := newDownloadStore(t.TempDir(), nil)
	rec := httptest.NewRecorder()
	body := strings.NewReader(`{"vol":"demo","path":"/a.txt"}`)
	handleAPIDownloads(dl)(rec, httptest.NewRequest(http.MethodPost, "/api/downloads", body))
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("POST = %d %s", rec.Code, rec.Body.String())
	}
}

// TestDownloadsOpenMethod 打开目录只接受 POST；GET 不触发系统调用。
func TestDownloadsOpenMethod(t *testing.T) {
	dl := newDownloadStore(t.TempDir(), nil)
	rec := httptest.NewRecorder()
	handleAPIDownloadsOpen(dl)(rec, httptest.NewRequest(http.MethodGet, "/api/downloads/open", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET = %d", rec.Code)
	}
}
