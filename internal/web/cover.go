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
	"errors"
	"io"
	"net/http"
	"strconv"

	"mytgfs/internal/cover"
	"mytgfs/internal/i18n"
)

// CoverSource 是频道头像的读写入口。
//
// 输入/输出：
//   - HasVolume 判断卷是否还挂着
//   - CoverVersion 返回头像版本（photo id，无头像为空串）
//   - CoverImage 返回头像字节与版本
//   - SetCover 写入一张已归一的正方形 JPEG
type CoverSource interface {
	HasVolume(name string) bool
	CoverVersion(ctx context.Context, volume string) (string, error)
	CoverImage(ctx context.Context, volume string) ([]byte, string, error)
	SetCover(ctx context.Context, volume string, data []byte) error
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
