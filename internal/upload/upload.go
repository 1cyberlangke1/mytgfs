// Package upload 是「算内容摘要 + 写时去重 + 上传」的唯一实现。
//
// 输入：索引与存储的窄接口、目标路径、内容读取器
// 输出：写入频道的消息（Part）、是否复用了已有 document
// 预期：
//   - 同目录已有同 size+sha256 的文件时发引用消息，零字节上传
//   - 复用失败（引用过期、对端异常）回退成正常上传，不影响正确性
//   - 同路径命中不算复用：覆盖写仍按上传处理，避免自己引用自己
package upload

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"path"

	"mytgfs/internal/i18n"
	"mytgfs/internal/index"
	"mytgfs/internal/telegramstore"
)

// ErrNoResult 表示上传流程没有产出消息（不应发生，防御性返回）。
// 用类型而不是包级变量：文案要按当前语言惰性求值。
type errNoResult struct{}

func (errNoResult) Error() string { return i18n.T("err.upload_no_result") }

var ErrNoResult error = errNoResult{}

// Index 是本包需要的索引能力：查同内容、写记录。
type Index interface {
	FindContent(ctx context.Context, dir string, size int64, sha string) (index.Node, bool, error)
}

// Store 是本包需要的存储能力。
type Store interface {
	Copy(ctx context.Context, src *telegramstore.Part, name, mime, caption string) (*telegramstore.Part, error)
	UploadResumable(ctx context.Context, r io.ReaderAt, size int64, key, name, mime, caption string) (*telegramstore.Part, error)
}

// Request 描述一次写入。
type Request struct {
	Path    string      // 卷内绝对路径
	Reader  io.ReaderAt // 内容，需支持从 0 读
	Size    int64       // 字节数
	SHA     string      // 内容 sha256（十六进制），同时作为续传键
	Mime    string      // MIME；空则用 application/octet-stream
	Caption string      // 已编码好的 caption
}

// Hash 计算内容摘要，供续传键与去重使用。
//
// 输入：r 支持随机读的内容；size 字节数
// 输出：sha256 十六进制串
func Hash(r io.ReaderAt, size int64) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, io.NewSectionReader(r, 0, size)); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Part 把节点记录转成存储层消息描述。
func Part(n index.Node) *telegramstore.Part {
	return &telegramstore.Part{
		MessageID:     n.ID,
		DocumentID:    n.DocumentID,
		AccessHash:    n.AccessHash,
		FileReference: n.FileReference,
		Size:          n.Size,
	}
}

// Write 写入一份内容：能复用就复用，否则上传。
//
// 输入：ctx；idx 索引；st 存储；req 写入描述
// 输出：频道消息、是否复用、错误
// 预期：复用命中时不产生任何上传字节；耗时只在真正的上传阶段
func Write(ctx context.Context, idx Index, st Store, req Request) (*telegramstore.Part, bool, error) {
	mime := req.Mime
	if mime == "" {
		mime = "application/octet-stream"
	}
	name := path.Base(req.Path)

	part, reused, err := find(ctx, idx, st, req.Path, req.Size, req.SHA, name, mime, req.Caption)
	if err != nil || reused {
		return part, reused, err
	}

	p, uerr := st.UploadResumable(ctx, req.Reader, req.Size, req.SHA, name, mime, req.Caption)
	if uerr != nil {
		return nil, false, uerr
	}
	if p == nil {
		return nil, false, ErrNoResult
	}
	return p, false, nil
}

// find 在同目录里找同内容的已有文件并复用其 document。
//
// 输出：hit 表示复用成功（part 非空）
func find(ctx context.Context, idx Index, st Store, dst string, size int64, sha, name, mime, caption string) (*telegramstore.Part, bool, error) {
	hit, ok, err := idx.FindContent(ctx, path.Dir(dst), size, sha)
	if err != nil {
		return nil, false, err
	}
	if !ok || hit.Path == dst || hit.DocumentID == 0 {
		return nil, false, nil
	}
	part, cerr := st.Copy(ctx, Part(hit), name, mime, caption)
	if cerr != nil {
		// 复用失败不影响正确性：回退成正常上传
		slog.Debug(i18n.T("log.dedup_fallback"), "path", dst, "src", hit.Path, "err", cerr)
		return nil, false, nil
	}
	return part, true, nil
}
