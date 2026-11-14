package web

// 移动/改名接口单测：前端改名时给的是完整目标路径（dest），
// 服务端必须用它，否则改名请求等于什么都没做。

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mytgfs/internal/fm"
)

// newMoveAPI 装配一个只用假文件后端的移动接口。
func newMoveAPI(f *fakeFiles) http.HandlerFunc {
	return handleAPIMove(&filesAPI{
		manager: func(string) (fm.Files, bool) { return f, true },
	})
}

// TestMoveRenameUsesDest 单条改名按 dest 生效。
//
// 输入：POST {paths:["/a.txt"], to:"/", dest:"/b.txt"}
// 输出：200；假后端里 /a.txt 变成 /b.txt
// 预期：忽略 dest 的旧实现会保持 /a.txt 不变
func TestMoveRenameUsesDest(t *testing.T) {
	f := newFakeFiles()
	f.uploads["/a.txt"] = "hello"
	h := newMoveAPI(f)

	body := `{"vol":"demo","paths":["/a.txt"],"to":"/","dest":"/b.txt"}`
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodPost, "/api/files/move", strings.NewReader(body)))

	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	if _, ok := f.uploads["/b.txt"]; !ok {
		t.Fatalf("未改名为 /b.txt: %v", f.uploads)
	}
	if _, ok := f.uploads["/a.txt"]; ok {
		t.Fatalf("旧名仍在: %v", f.uploads)
	}
}

// TestMoveDestOnlyForSinglePath 批量移动时 dest 不生效，名称保持不变。
//
// 输入：两条路径 + dest
// 输出：两个文件都保留原名
// 预期：避免批量操作被一条 dest 全改成同一个名字
func TestMoveDestOnlyForSinglePath(t *testing.T) {
	f := newFakeFiles()
	f.uploads["/a.txt"] = "a"
	f.uploads["/b.txt"] = "b"
	h := newMoveAPI(f)

	body := `{"vol":"demo","paths":["/a.txt","/b.txt"],"to":"/dir","dest":"/dir/same.txt"}`
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodPost, "/api/files/move", strings.NewReader(body)))

	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	if _, ok := f.uploads["/dir/a.txt"]; !ok {
		t.Fatalf("a.txt 未按原名移动: %v", f.uploads)
	}
	if _, ok := f.uploads["/dir/b.txt"]; !ok {
		t.Fatalf("b.txt 未按原名移动: %v", f.uploads)
	}
}

// TestMoveRejectsBadDest 非法目标路径必须拒绝。
//
// 输入：dest 含 ..
// 输出：400
// 预期：不允许跳出卷根
func TestMoveRejectsBadDest(t *testing.T) {
	f := newFakeFiles()
	h := newMoveAPI(f)

	for _, dest := range []string{"/../x", "relative.txt", "/"} {
		body := `{"vol":"demo","paths":["/a.txt"],"to":"/","dest":"` + dest + `"}`
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest(http.MethodPost, "/api/files/move", strings.NewReader(body)))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("dest=%q code=%d body=%s", dest, rec.Code, rec.Body.String())
		}
	}
}
