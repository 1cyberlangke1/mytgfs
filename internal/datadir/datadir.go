// Package datadir 统一存放运行时文件（索引、日志、进程状态）。
//
// 输入：文件名
// 输出：data/<文件名> 的相对路径
// 预期：目录不存在时自动创建，避免运行时文件散落在项目根目录
package datadir

import (
	"os"
	"path/filepath"
)

// Dir 是运行时文件目录名。
const Dir = "data"

// Path 返回 data 目录下的路径并确保目录存在。
//
// 输入：name 文件名或子路径
// 输出：相对路径；目录创建失败时仍返回路径，由使用方报错
func Path(name string) string {
	p := filepath.Join(Dir, name)
	_ = os.MkdirAll(filepath.Dir(p), 0o700)
	return p
}
