package web

// 批量删除的部分失败：成功的照常删，失败的逐条回报，不能整批回滚或静默。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mytgfs/internal/fm"
)

// partialFiles 让 /locked 删除失败，其余走内存后端。
type partialFiles struct {
	*fakeFiles
}

func (p *partialFiles) Remove(ctx context.Context, path string) error {
	if path == "/locked" {
		return errors.New("locked")
	}
	return p.fakeFiles.Remove(ctx, path)
}

// newPartialAPI 构造只认 demo 卷的 filesAPI。
func newPartialAPI(t *testing.T, files *partialFiles) *filesAPI {
	t.Helper()
	return newFilesAPI(
		func(volume string) (fm.Files, bool) {
			if volume != "demo" {
				return nil, false
			}
			return files, true
		},
		func() []string { return []string{"demo"} },
		nil,
		t.TempDir(),
	)
}

// TestRemovePartialFailureReportsEachPath 一条成功一条失败时返回 200 + 失败清单。
func TestRemovePartialFailureReportsEachPath(t *testing.T) {
	files := &partialFiles{newFakeFiles()}
	files.uploads["/ok"] = "x"
	files.uploads["/locked"] = "y"
	api := newPartialAPI(t, files)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/files/remove",
		strings.NewReader(`{"vol":"demo","paths":["/ok","/locked"]}`))
	handleAPIRemove(api)(rec, req)

	var resp struct {
		Removed int           `json:"removed"`
		Failed  []pathFailure `json:"failed"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应: %v（body=%s）", err, rec.Body.String())
	}
	if rec.Code != http.StatusOK || resp.Removed != 1 {
		t.Fatalf("code=%d removed=%d body=%s", rec.Code, resp.Removed, rec.Body.String())
	}
	if len(resp.Failed) != 1 || resp.Failed[0].Path != "/locked" {
		t.Fatalf("失败清单 = %+v", resp.Failed)
	}
	if _, stillThere := files.uploads["/ok"]; stillThere {
		t.Fatal("成功项应已删除")
	}
}

// TestRemoveAllFailedReturnsBadGateway 全失败时用 502，让前端知道整批没成。
func TestRemoveAllFailedReturnsBadGateway(t *testing.T) {
	files := &partialFiles{newFakeFiles()}
	files.uploads["/locked"] = "y"
	api := newPartialAPI(t, files)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/files/remove",
		strings.NewReader(`{"vol":"demo","paths":["/locked"]}`))
	handleAPIRemove(api)(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("code=%d，期望 502（body=%s）", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "/locked") {
		t.Fatalf("响应应逐条带上失败路径: %s", rec.Body.String())
	}
	if got := fmt.Sprint(len(files.uploads)); got != "1" {
		t.Fatalf("失败项不应被删除，uploads=%v", files.uploads)
	}
}
