package vfs

// 挂载层已移除：Windows 侧改用 `mytgfs webdav`，不再依赖 WinFsp/FUSE。
// 这里只保留调用点需要的空实现，挂载调用会直接返回错误。

import (
	"context"
	"errors"

	"mytgfs/internal/index"
)

// Transfer 保留给面板展示；挂载移除后不再产生数据。
type Transfer struct {
	Path  string `json:"path"`
	Done  int64  `json:"done"`
	Total int64  `json:"total"`
}

// FS 是挂载层的占位实现。
type FS struct{}

// New 创建占位文件系统。
func New(_ context.Context, _ *index.Index, _ any, _ string) *FS { return &FS{} }

// NotifyPaths 不再需要通知资源管理器。
func (f *FS) NotifyPaths(_, _ []string) {}

// ResumePending 不再有挂载期补传；崩溃恢复由 webdav 上传路径负责。
func (f *FS) ResumePending() {}

// Transfers 没有挂载就没有下载进度。
func (f *FS) Transfers() []Transfer { return nil }

// Mount 已移除，直接返回错误提示改用 WebDAV。
func Mount(_ context.Context, _ *FS, _, _ string) error {
	return errors.New("FUSE 挂载已移除，请使用 mytgfs webdav")
}
