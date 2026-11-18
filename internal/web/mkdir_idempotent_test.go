package web

// 建目录接口单测：导入流程会对每个目录先建一次，重复导入同一棵树
// 必须按成功处理，否则会把已存在的目录误报成失败。

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mytgfs/internal/fm"
)

// newMkdirAPI 装配一个只用假文件后端的建目录接口。
func newMkdirAPI(f *fakeFiles) http.HandlerFunc {
	return handleAPIMkdir(&filesAPI{
		manager: func(string) (fm.Files, bool) { return f, true },
	})
}

// TestMkdirIdempotent 同名目录连建两次都返回 200。
//
// 输入：POST {vol,dir,name} 两次相同参数
// 输出：两次都是 200，假后端里只留一个目录
// 预期：第二次落到 fm.ErrExists 时不再回 502
func TestMkdirIdempotent(t *testing.T) {
	f := newFakeFiles()
	h := newMkdirAPI(f)

	body := `{"vol":"demo","dir":"/","name":"imptest"}`
	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest(http.MethodPost, "/api/files/mkdir", strings.NewReader(body)))
		if rec.Code != http.StatusOK {
			t.Fatalf("第 %d 次 code=%d body=%s", i+1, rec.Code, rec.Body.String())
		}
	}
	if !f.dirs["/imptest"] {
		t.Fatalf("目录没有建出来: %v", f.dirs)
	}
	if len(f.dirs) != 1 {
		t.Fatalf("应只有一个目录，实际 %v", f.dirs)
	}
}

// TestMkdirBadPathStillFails 卷根等非法路径仍然报错。
//
// 输入：name 为空
// 输出：400
// 预期：幂等只针对「已存在」，不放宽非法参数
func TestMkdirBadPathStillFails(t *testing.T) {
	f := newFakeFiles()
	h := newMkdirAPI(f)

	body := `{"vol":"demo","dir":"/","name":""}`
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodPost, "/api/files/mkdir", strings.NewReader(body)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
}
