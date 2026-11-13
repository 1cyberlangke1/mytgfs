package web

// 分片上传接口单测：会话建立、乱序拒绝、提交入库与放弃清理。

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"mytgfs/internal/fm"
)

// newTestUploadAPI 装配一个只用假文件后端的分片上传接口。
func newTestUploadAPI(t *testing.T, f *fakeFiles) *uploadAPI {
	t.Helper()
	api := newUploadAPI(func(string) (fm.Files, bool) { return f, true }, t.TempDir())
	// Windows 上未关闭的临时文件会挡住 TempDir 清理，测试结束统一放弃会话
	t.Cleanup(func() {
		api.mu.Lock()
		ids := make([]string, 0, len(api.sess))
		for id := range api.sess {
			ids = append(ids, id)
		}
		api.mu.Unlock()
		for _, id := range ids {
			api.drop(id)
		}
	})
	return api
}

// initSession 建会话并返回 id。
func initSession(t *testing.T, api *uploadAPI, body string) string {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/upload/init", strings.NewReader(body))
	api.handleInit(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("init code = %d body = %s", rec.Code, rec.Body.String())
	}
	var out struct{ ID string }
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.ID == "" {
		t.Fatalf("init 响应缺 id: %s", rec.Body.String())
	}
	return out.ID
}

// putChunk 发一段内容。
func putChunk(t *testing.T, api *uploadAPI, id string, offset int, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/upload/chunk?id="+id+"&offset="+itoa(offset), strings.NewReader(body))
	api.handleChunk(rec, req)
	return rec
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// TestUploadAPIFlow 两段上传后提交，内容与路径都正确。
func TestUploadAPIFlow(t *testing.T) {
	f := newFakeFiles()
	api := newTestUploadAPI(t, f)
	id := initSession(t, api, `{"vol":"demo","dir":"/","rel":"sub/a.txt","name":"a.txt","size":11}`)

	if rec := putChunk(t, api, id, 0, "hello"); rec.Code != http.StatusOK {
		t.Fatalf("第一段 code = %d", rec.Code)
	}
	if rec := putChunk(t, api, id, 5, "-world"); rec.Code != http.StatusOK {
		t.Fatalf("第二段 code = %d", rec.Code)
	}

	rec := httptest.NewRecorder()
	api.handleCommit(rec, httptest.NewRequest(http.MethodPost, "/api/upload/commit?id="+id, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("commit code = %d body = %s", rec.Code, rec.Body.String())
	}
	if got := f.uploads["/sub/a.txt"]; got != "hello-world" {
		t.Fatalf("落盘内容不符: %q", got)
	}
	api.mu.Lock()
	_, alive := api.sess[id]
	api.mu.Unlock()
	if alive {
		t.Fatal("提交后会话应被清理")
	}
}

// TestUploadAPIOffsetConflict offset 不连续时返回 409 与当前进度。
func TestUploadAPIOffsetConflict(t *testing.T) {
	f := newFakeFiles()
	api := newTestUploadAPI(t, f)
	id := initSession(t, api, `{"vol":"demo","dir":"/","name":"a.bin","size":4}`)
	putChunk(t, api, id, 0, "ab")

	rec := putChunk(t, api, id, 0, "cd")
	if rec.Code != http.StatusConflict {
		t.Fatalf("code = %d，期望 409", rec.Code)
	}
	var out struct{ Offset int64 }
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.Offset != 2 {
		t.Fatalf("应回报当前进度 2: %s", rec.Body.String())
	}
}

// TestUploadAPIIncomplete 没传够就提交应被拒绝并清理会话。
func TestUploadAPIIncomplete(t *testing.T) {
	f := newFakeFiles()
	api := newTestUploadAPI(t, f)
	id := initSession(t, api, `{"vol":"demo","dir":"/","name":"a.bin","size":9}`)
	putChunk(t, api, id, 0, "abc")

	rec := httptest.NewRecorder()
	api.handleCommit(rec, httptest.NewRequest(http.MethodPost, "/api/upload/commit?id="+id, nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d，期望 400", rec.Code)
	}
	if len(f.uploads) != 0 {
		t.Fatalf("不应入库: %v", f.uploads)
	}
}

// TestUploadAPIAbort 放弃会话后临时文件必须消失。
func TestUploadAPIAbort(t *testing.T) {
	f := newFakeFiles()
	api := newTestUploadAPI(t, f)
	id := initSession(t, api, `{"vol":"demo","dir":"/","name":"a.bin","size":4}`)
	putChunk(t, api, id, 0, "ab")

	api.mu.Lock()
	name := api.sess[id].file.Name()
	api.mu.Unlock()

	rec := httptest.NewRecorder()
	api.handleAbort(rec, httptest.NewRequest(http.MethodDelete, "/api/upload?id="+id, nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("code = %d", rec.Code)
	}
	if _, err := os.Stat(name); !os.IsNotExist(err) {
		t.Fatalf("临时文件应被删除: %v", err)
	}
}

// TestUploadAPIRejectsTraversal 越界相对路径在 init 阶段就拒绝。
func TestUploadAPIRejectsTraversal(t *testing.T) {
	f := newFakeFiles()
	api := newTestUploadAPI(t, f)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/upload/init",
		bytes.NewReader([]byte(`{"vol":"demo","dir":"/","rel":"../x.txt","size":1}`)))
	api.handleInit(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d，期望 400", rec.Code)
	}
}

// TestUploadAPIStatus 查询进度用于断线续传。
func TestUploadAPIStatus(t *testing.T) {
	f := newFakeFiles()
	api := newTestUploadAPI(t, f)
	id := initSession(t, api, `{"vol":"demo","dir":"/","name":"a.bin","size":6}`)
	putChunk(t, api, id, 0, "abc")

	rec := httptest.NewRecorder()
	api.handleStatus(rec, httptest.NewRequest(http.MethodGet, "/api/upload?id="+id, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d", rec.Code)
	}
	var out struct{ Offset int64 }
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.Offset != 3 {
		t.Fatalf("进度不符: %s", rec.Body.String())
	}
}
