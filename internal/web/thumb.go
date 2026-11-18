package web

// 缩略图与文件详情。
//
// 只在用户点「详情」时才请求图片：列表页不预取，避免为大目录下载整批图片。
// 生成结果按 ETag 缓存到磁盘，浏览器也按同一 ETag 缓存。

import (
	"bytes"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"log/slog"
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
	// thumbMaxPixels 限制解码后的像素总数：高压缩比的 PNG 可以几 MB 解出上亿像素，
	// 不设上限等于允许一张图把进程内存打满
	thumbMaxPixels = 40_000_000
	// thumbWorkers 是同时解码的图片数上限：并发太高时多张大图叠加同样会顶爆内存
	thumbWorkers = 2
)

// thumbExts 是允许生成缩略图的扩展名。
var thumbExts = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true,
}

// thumbCache 是磁盘缓存目录。
//
// 并发策略：
//   - inflight 让同一张图只生成一次，其余请求等这一次的结果
//   - sem 限制同时解码的图片数，避免多张大图叠加把内存顶爆
//
// 早先这里用一把全局锁把「读缓存 + 远端读 + 解码 + 编码」全串起来，
// 结果是任何一张图的生成都会卡住其它所有卷的缩略图。
type thumbCache struct {
	dir string

	mu       sync.Mutex
	inflight map[string]*thumbGen
	sem      chan struct{}
}

// thumbGen 是一次正在进行的缩略图生成。
type thumbGen struct {
	done chan struct{}
	data []byte
}

func newThumbCache(dir string) *thumbCache {
	if strings.TrimSpace(dir) == "" {
		dir = "data/thumbs"
	}
	return &thumbCache{
		dir:      dir,
		inflight: map[string]*thumbGen{},
		sem:      make(chan struct{}, thumbWorkers),
	}
}

// thumbRootDir 返回缓存根目录（空值回落默认）。
func thumbRootDir(dir string) string {
	if strings.TrimSpace(dir) == "" {
		return "data/thumbs"
	}
	return dir
}

// volDir 返回一个卷的缓存子目录。
func (c *thumbCache) volDir(vol string) string {
	return filepath.Join(c.dir, vol)
}

// thumbCache 按卷分目录存放缩略图：thumbs/<vol>/<路径哈希>-<内容哈希>.jpg。
// 分卷让删卷可以整块清理，路径哈希让删文件/改名可以按前缀清理。

// pathHash 是卷内路径的缓存键前缀：同一路径的全部缓存文件共享前缀。
func pathHash(vol, p string) string {
	return fmt.Sprintf("%x", sha1.Sum([]byte(vol+"\x00"+p)))
}

// key 由「卷 + 路径 + 大小 + 修改时间」算出缓存键。
//
// 文件被覆盖后大小或时间会变，键随之变化，旧缩略图自然失效。
func thumbKey(vol, p string, e fm.Entry) string {
	h := sha256.Sum256(fmt.Appendf(nil, "%s\x00%s\x00%d\x00%d", vol, p, e.Size, e.Mod.UnixNano()))
	return hex.EncodeToString(h[:16])
}

// purgeEntry 删掉卷内一个路径的全部缓存文件（缩略图/封面，所有版本）。
// 返回删除的文件数；未接线（nil）时为无害空操作。
func (c *thumbCache) purgeEntry(vol, p string) int {
	if c == nil {
		return 0
	}
	ph := pathHash(vol, p)
	stale, _ := filepath.Glob(filepath.Join(c.volDir(vol), ph+"-*"))
	removed := 0
	for _, f := range stale {
		if os.Remove(f) == nil {
			removed++
		}
	}
	return removed
}

// obtain 返回缩略图字节：命中缓存直接读，未命中调用 gen 生成并写缓存。
//
// 输入：vol 卷名；p 卷内路径；key 版本键；gen 生成函数
// 输出：JPEG 字节；生成失败返回 nil
// 预期：
//   - 同一个 key 只允许一个生成者，其余调用者等它的结果
//   - 同时解码数受 thumbWorkers 限制，避免多张大图叠加
func (c *thumbCache) obtain(vol, p, key string, gen func() []byte) []byte {
	if b := c.load(vol, p, key); b != nil {
		return b
	}
	c.mu.Lock()
	if g, ok := c.inflight[key]; ok {
		c.mu.Unlock()
		<-g.done
		return g.data
	}
	g := &thumbGen{done: make(chan struct{})}
	c.inflight[key] = g
	c.mu.Unlock()

	// sem 用闭包包住，生成过程中 panic 也不会漏放名额
	b := func() []byte {
		c.sem <- struct{}{}
		defer func() { <-c.sem }()
		return gen()
	}()
	if b != nil {
		c.save(vol, p, key, b)
	}
	g.data = b
	close(g.done)

	c.mu.Lock()
	delete(c.inflight, key)
	c.mu.Unlock()
	return b
}

// load 返回缓存文件内容；未命中返回 nil。
func (c *thumbCache) load(vol, p, key string) []byte {
	b, err := os.ReadFile(filepath.Join(c.volDir(vol), pathHash(vol, p)+"-"+key+".jpg"))
	if err != nil {
		return nil
	}
	return b
}

// save 原子写入缓存；失败只影响下次重新生成。
// 同路径的旧键文件顺手清掉：覆盖写不会留孤儿。
//
// 输入：vol 卷名；p 卷内路径；key 版本键；data JPEG 字节
// 输出：无
// 预期：目录创建或写盘失败时记 slog 告警后返回（下次请求重新生成即可）；
//
//	成功写入后清理同路径其它版本文件
func (c *thumbCache) save(vol, p, key string, data []byte) {
	volDir := c.volDir(vol)
	if err := os.MkdirAll(volDir, 0o755); err != nil {
		slog.Warn(i18n.T("log.thumb_cache_failed"), "vol", vol, "path", p, "err", err)
		return
	}
	target := filepath.Join(volDir, pathHash(vol, p)+"-"+key+".jpg")
	if err := atomic.WriteFile(target, bytes.NewReader(data)); err != nil {
		slog.Warn(i18n.T("log.thumb_cache_failed"), "vol", vol, "path", p, "err", err)
		return
	}
	stale, _ := filepath.Glob(filepath.Join(volDir, pathHash(vol, p)+"-*.jpg"))
	for _, f := range stale {
		if f != target {
			_ = os.Remove(f)
		}
	}
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

		if b := api.thumbs.load(vol, p, key); b != nil {
			writeThumb(w, b)
			return
		}

		// 命中磁盘缓存直接返回；未命中时同一张图只生成一次，
		// 其它请求等这一次结果，不同图片之间互不阻塞
		b := api.thumbs.obtain(vol, p, key, func() []byte {
			return generateThumb(r, m, p, thumbMaxSide)
		})
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
	// 缩略图/海报是预览读取，进度不当作下载任务
	if s, ok := f.(interface{ MarkStream() }); ok {
		s.MarkStream()
	}
	// 先只读文件头拿尺寸：几 MB 的高压缩比 PNG 能解出上亿像素，
	// 直接 Decode 等于让一张图把进程内存打满
	cfg, _, cerr := image.DecodeConfig(f)
	if cerr != nil {
		return nil
	}
	if int64(cfg.Width)*int64(cfg.Height) > thumbMaxPixels {
		return nil
	}
	if _, serr := f.Seek(0, io.SeekStart); serr != nil {
		return nil
	}
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
