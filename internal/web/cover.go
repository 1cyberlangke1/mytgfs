package web

// 卷封面：直接用 Telegram 频道头像，不再读卷里的特殊命名文件。
//
// GET  /api/cover?vol=  输出频道头像（ETag = photo id）
// POST /api/cover?vol=  上传一张原图，服务端裁方压缩后写回频道头像
//
// 头像在 Telegram 侧，别的客户端改了头像也会同步过来；
// 面板只负责展示与发起修改。

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"mytgfs/internal/cover"
	"mytgfs/internal/i18n"
)

// bodyFromSrc 让「按路径上传」复用同一套读取逻辑。
//
// 输入：r；查询参数 src 为宿主落盘的绝对路径（原生选择器/原生落盘通道）
// 输出：无；src 有效时把 r.Body 换成文件句柄
// 预期：src 缺失或不可读时保持 r.Body 不变，后续按原逻辑返回 400；
// 路径必须是绝对路径且不含 NUL，避免被拼成任意特殊文件
func bodyFromSrc(r *http.Request) {
	src := r.URL.Query().Get("src")
	if src == "" || strings.ContainsRune(src, 0) {
		return
	}
	abs, err := filepath.Abs(src)
	if err != nil || abs != src {
		// 只接受调用方给出的绝对路径形态，不做相对路径解析
		return
	}
	f, err := os.Open(abs)
	if err != nil {
		return
	}
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		f.Close()
		return
	}
	r.Body = f
}

// CoverSource 是频道头像与标题的读写入口。
//
// 输入/输出：
//   - HasVolume 判断卷是否还挂着
//   - CoverVersion 返回头像版本（photo id，无头像为空串）
//   - CoverImage 返回头像字节与版本
//   - SetCover 写入一张已归一的正方形 JPEG
//   - Title 返回频道标题（面板显示名）
type CoverSource interface {
	HasVolume(name string) bool
	CoverVersion(ctx context.Context, volume string) (string, error)
	CoverImage(ctx context.Context, volume string) ([]byte, string, error)
	SetCover(ctx context.Context, volume string, data []byte) error
	Title(ctx context.Context, volume string) (string, error)
	// SetTitle 改频道标题（面板显示名）
	SetTitle(ctx context.Context, volume, title string) error
}

// maxCoverUpload 是面板上传头像的原始图片上限。
const maxCoverUpload = 16 << 20

// handleVolumeCover 读写频道头像。
//
// 输入：covers 头像源；GET 读图，POST 上传原图
// 输出：GET image/jpeg；POST {"ok":true}；404 无头像/无卷；501 未接线
// 预期：
//   - ETag 用 photo id，别人换头像后 URL 版本变化，浏览器不会吃旧缓存
//   - POST 的 body 是原始图片字节（非 multipart），由 cover.SquareJPEG 归一
func handleVolumeCover(covers CoverSource) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if covers == nil {
			http.Error(w, i18n.T("err.http_no_cover"), http.StatusNotImplemented)
			return
		}
		vol := r.URL.Query().Get("vol")
		if !covers.HasVolume(vol) {
			http.Error(w, i18n.T("err.http_no_volume"), http.StatusNotFound)
			return
		}
		switch r.Method {
		case http.MethodGet, http.MethodHead:
			serveCover(w, r, covers, vol)
		case http.MethodPost:
			uploadCover(w, r, covers, vol)
		case http.MethodPut:
			updateTitle(w, r, covers, vol)
		default:
			http.Error(w, i18n.T("err.http_method"), http.StatusMethodNotAllowed)
		}
	}
}

// serveCover 输出频道头像。
func serveCover(w http.ResponseWriter, r *http.Request, covers CoverSource, vol string) {
	data, version, err := covers.CoverImage(r.Context(), vol)
	if err != nil {
		http.Error(w, i18n.T("err.cover_fetch"), http.StatusBadGateway)
		return
	}
	if len(data) == 0 || version == "" {
		http.Error(w, i18n.T("err.http_not_found"), http.StatusNotFound)
		return
	}
	w.Header().Set("ETag", `"`+version+`"`)
	w.Header().Set("Cache-Control", "private, max-age=604800")
	if etagMatches(r.Header.Get("If-None-Match"), version) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(data)
}

// uploadCover 接收原图并写回频道头像。
//
// 输入：r.Body 原始图片字节
// 输出：{"ok":true}；400 空体；413 超限；415 不是图片；502 Telegram 拒绝
// 预期：先本地归一成 512×512 JPEG，Telegram 侧只会收到合规头像
func uploadCover(w http.ResponseWriter, r *http.Request, covers CoverSource, vol string) {
	bodyFromSrc(r)
	body := http.MaxBytesReader(w, r.Body, maxCoverUpload)
	raw, err := io.ReadAll(body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			http.Error(w, i18n.T("err.cover_too_large"), http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, i18n.T("err.http_bad_request"), http.StatusBadRequest)
		return
	}
	if len(raw) == 0 {
		http.Error(w, i18n.T("err.cover_empty"), http.StatusBadRequest)
		return
	}
	jpeg, err := cover.SquareJPEG(raw)
	if err != nil {
		http.Error(w, i18n.T("err.cover_decode"), http.StatusUnsupportedMediaType)
		return
	}
	if err := covers.SetCover(r.Context(), vol, jpeg); err != nil {
		http.Error(w, i18n.T("err.cover_set"), http.StatusBadGateway)
		return
	}
	replyJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// updateTitle 改频道标题（面板显示名）。
//
// 输入：body {"title":"新名字"}
// 输出：{"ok":true}；400 体非法或标题为空；502 Telegram 拒绝
// 预期：只改频道标题，不动节点数据；成功由路由层自动推送 status；
// 错误统一走 JSON，前端只会解析 JSON
func updateTitle(w http.ResponseWriter, r *http.Request, covers CoverSource, vol string) {
	var req struct {
		Title string `json:"title"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		replyJSONError(w, http.StatusBadRequest, i18n.T("err.web_bad_json"))
		return
	}
	if strings.TrimSpace(req.Title) == "" {
		replyJSONError(w, http.StatusBadRequest, i18n.T("err.title_empty"))
		return
	}
	if err := covers.SetTitle(r.Context(), vol, req.Title); err != nil {
		// 权限不足、频道不可用等上游原因写日志，面板只显示人话
		slog.Warn(i18n.T("log.title_set_failed"), "vol", vol, "err", err)
		replyJSONError(w, http.StatusBadGateway, i18n.T("err.title_set"))
		return
	}
	replyJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
