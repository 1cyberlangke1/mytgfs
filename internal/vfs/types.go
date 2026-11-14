package vfs

// 文件系统对外暴露的最小接口：面板与命令行只依赖这两个类型，
// 不引入任何具体网络文件系统协议的依赖。

import (
	"context"
	"io"
	"io/fs"
	"os"
)

// File 是打开中的文件或目录句柄。
type File interface {
	io.Closer
	io.Reader
	io.Writer
	io.Seeker
	Readdir(count int) ([]fs.FileInfo, error)
	Stat() (fs.FileInfo, error)
}

// FS 是卷内的文件系统能力，由 *FileSystem 实现。
type FS interface {
	Mkdir(ctx context.Context, name string, perm os.FileMode) error
	OpenFile(ctx context.Context, name string, flag int, perm os.FileMode) (File, error)
	RemoveAll(ctx context.Context, name string) error
	Rename(ctx context.Context, oldName, newName string) error
	Stat(ctx context.Context, name string) (fs.FileInfo, error)
}
