package web

// 缩略图与文件详情。
//
// 只在用户点「详情」时才请求图片：列表页不预取，避免为大目录下载整批图片。
// 生成结果按 ETag 缓存到磁盘，浏览器也按同一 ETag 缓存。

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	"image/jpeg"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	xdraw "golang.org/x/image/draw"

	_ "golang.org/x/image/webp"
	_ "image/gif"
	_ "image/png"

	"github.com/natefinch/atomic"

	"mytgfs/internal/fm"
	"mytgfs/internal/i18n"
)

const (
	// thumbMaxSource 超过这个大小的图片不做缩略图，避免为预览拉整个大文件
	thumbMaxSource = 16 << 20
	// thumbMaxSide 缩略图最长边
	thumbMaxSide = 320
	// coverMaxSide 卷封面最长边：卡片头图约 400px 宽，2x 屏取 720
	coverMaxSide = 720
)

// thumbExts 是允许生成缩略图的扩展名。
var thumbExts = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true,
}

// thumbCache 是磁盘缓存目录，带锁避免同一张图被并发重复生成。
type thumbCache struct {
	dir string
	mu  sync.Mutex
}

func newThumbCache(dir string) *thumbCache {
	if strings.TrimSpace(dir) == "" {
		dir = "data/thumbs"
	}
	return &thumbCache{dir: dir}
}

// key 由「卷 + 路径 + 大小 + 修改时间」算出缓存键。
//
// 文件被覆盖后大小或时间会变，键随之变化，旧缩略图自然失效。
func thumbKey(vol, p string, e fm.Entry) string {
	h := sha256.Sum256(fmt.Appendf(nil, "%s\x00%s\x00%d\x00%d", vol, p, e.Size, e.Mod.UnixNano()))
	return hex.EncodeToString(h[:16])
}

// load 返回缓存文件内容；未命中返回 nil。
func (c *thumbCache) load(key string) []byte {
	b, err := os.ReadFile(filepath.Join(c.dir, key+".jpg"))
	if err != nil {
		return nil
	}
	return b
}

// save 原子写入缓存；失败只影响下次重新生成。
func (c *thumbCache) save(key string, data []byte) {
	_ = os.MkdirAll(c.dir, 0o755)
	_ = atomic.WriteFile(filepath.Join(c.dir, key+".jpg"), bytes.NewReader(data))
}

// handleFileThumb 输出缩略图（GET /files/thumb?vol=&path=）。
//
// 输入：vol 卷名；path 卷内路径
// 输出：image/jpeg；命中 If-None-Match 时 304
// 预期：
//   - 非图片扩展名或超过大小上限返回 415，前端回落到图标
//   - 响应头带 ETag 与长缓存，同一张图浏览器只取一次
func handleFileThumb(api *filesAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if api == nil {
			http.Error(w, i18n.T("err.http_no_files"), http.StatusNotImplemented)
			return
		}
		q := r.URL.Query()
		vol, p := q.Get("vol"), q.Get("path")
		if !thumbExts[strings.ToLower(path.Ext(p))] {
			http.Error(w, i18n.T("err.http_not_image"), http.StatusUnsupportedMediaType)
			return
		}
		m, ok := api.manager(vol)
		if !ok {
			http.Error(w, i18n.T("err.http_no_volume"), http.StatusNotFound)
			return
		}
		e, err := m.Info(r.Context(), p)
		if err != nil || e.Dir {
			http.Error(w, i18n.T("err.http_not_found"), http.StatusNotFound)
			return
		}
		if e.Size > thumbMaxSource {
			http.Error(w, i18n.T("err.http_too_large"), http.StatusUnsupportedMediaType)
			return
		}

		key := thumbKey(vol, p, e)
		w.Header().Set("ETag", `"`+key+`"`)
		w.Header().Set("Cache-Control", "private, max-age=604800")
		if etagMatches(r.Header.Get("If-None-Match"), key) {
			w.WriteHeader(http.StatusNotModified)
			return
		}

		if b := api.thumbs.load(key); b != nil {
			writeThumb(w, b)
			return
		}

		// 串行生成：目录里几十张图同时点开也不会把内存顶爆
		api.thumbs.mu.Lock()
		b := api.thumbs.load(key)
		if b == nil {
			b = generateThumb(r, m, p, thumbMaxSide)
			if b != nil {
				api.thumbs.save(key, b)
			}
		}
		api.thumbs.mu.Unlock()
		if b == nil {
			http.Error(w, i18n.T("err.http_decode_failed"), http.StatusUnsupportedMediaType)
			return
		}
		writeThumb(w, b)
	}
}

// generateThumb 读取远端文件并缩放成 JPEG；失败返回 nil。
//
// 输入：r 请求（用于 ctx）；m 文件后端；p 路径；side 最长边像素
// 输出：JPEG 字节；解码或读取失败返回 nil
func generateThumb(r *http.Request, m fm.Files, p string, side int) []byte {
	f, err := m.Open(r.Context(), p)
	if err != nil {
		return nil
	}
	defer f.Close()
	src, _, err := image.Decode(f)
	if err != nil {
		return nil
	}
	dst := scaleThumb(src, side)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 82}); err != nil {
		return nil
	}
	return buf.Bytes()
}

// scaleThumb 等比缩放到指定最长边。
func scaleThumb(src image.Image, side int) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return src
	}
	if w <= side && h <= side {
		return src
	}
	nw, nh := side, side
	if w >= h {
		nh = max(1, h*side/w)
	} else {
		nw = max(1, w*side/h)
	}
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, b, xdraw.Src, nil)
	return dst
}

// writeThumb 输出 JPEG 并带上内容长度。
func writeThumb(w http.ResponseWriter, data []byte) {
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Content-Length", fmt.Sprint(len(data)))
	_, _ = w.Write(data)
}

// etagMatches 处理 If-None-Match：支持逗号分隔与 W/ 弱校验前缀。
func etagMatches(header, key string) bool {
	for part := range strings.SplitSeq(header, ",") {
		part = strings.TrimSpace(part)
		part = strings.TrimPrefix(part, "W/")
		if part == `"`+key+`"` || part == key {
			return true
		}
	}
	return false
}
