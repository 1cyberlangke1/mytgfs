package web

// 分页测试：大目录只渲染一页，页码越界自动收敛。

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mytgfs/internal/fm"
)

// manyFiles 往假后端塞 n 个文件到根目录。
func manyFiles(f *fakeFiles, n int) {
	list := make([]fm.Entry, 0, n)
	for i := 0; i < n; i++ {
		list = append(list, fm.Entry{Name: fmt.Sprintf("f%04d.txt", i), Size: int64(i)})
	}
	f.entries["/"] = list
}

// TestBrowserPaginates 超过单页上限时只渲染一页并给出翻页信息。
func TestBrowserPaginates(t *testing.T) {
	f := newFakeFiles()
	manyFiles(f, filesPageSize+50)
	api := newTestAPI(f)

	rec := httptest.NewRecorder()
	handleFragFiles(api)(rec, httptest.NewRequest(http.MethodGet, "/frag/files?vol=demo&dir=%2F", nil))
	body := rec.Body.String()
	if !strings.Contains(body, "1 / 2") {
		t.Errorf("缺少页码信息: %s", body)
	}
	if !strings.Contains(body, "f0199.txt") {
		t.Errorf("第一页应包含第 %d 个文件", filesPageSize)
	}
	if strings.Contains(body, "f0200.txt") {
		t.Errorf("第一页不应包含第二页的文件")
	}
}

// TestBrowserSecondPage 第二页拿到剩余条目与上一页按钮。
func TestBrowserSecondPage(t *testing.T) {
	f := newFakeFiles()
	manyFiles(f, filesPageSize+50)
	api := newTestAPI(f)

	rec := httptest.NewRecorder()
	handleFragFiles(api)(rec, httptest.NewRequest(http.MethodGet,
		"/frag/files?vol=demo&dir=%2F&page=2", nil))
	body := rec.Body.String()
	if !strings.Contains(body, "2 / 2") {
		t.Errorf("缺少页码信息: %s", body)
	}
	if !strings.Contains(body, fmt.Sprintf("f%04d.txt", filesPageSize)) {
		t.Errorf("第二页缺少首个文件")
	}
	if strings.Contains(body, "f0000.txt") {
		t.Errorf("第二页不应包含第一页的文件")
	}
}

// TestBrowserPageClamped 页码越界收敛到最后一页，不 panic。
func TestBrowserPageClamped(t *testing.T) {
	f := newFakeFiles()
	manyFiles(f, 10)
	api := newTestAPI(f)

	rec := httptest.NewRecorder()
	handleFragFiles(api)(rec, httptest.NewRequest(http.MethodGet, "/frag/files?vol=demo&dir=%2F&page=99", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "f0009.txt") {
		t.Errorf("越界页码应回落到含数据的页")
	}
}

// TestBrowserNoPagerWhenSinglePage 单页不渲染翻页条。
func TestBrowserNoPagerWhenSinglePage(t *testing.T) {
	f := newFakeFiles()
	manyFiles(f, 3)
	api := newTestAPI(f)

	rec := httptest.NewRecorder()
	handleFragFiles(api)(rec, httptest.NewRequest(http.MethodGet, "/frag/files?vol=demo&dir=%2F", nil))
	if strings.Contains(rec.Body.String(), `class="pager"`) {
		t.Error("单页不应出现翻页条")
	}
}
