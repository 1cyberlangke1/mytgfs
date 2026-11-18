// Package datadir 统一存放运行时文件（索引、日志、进程状态）。
//
// 输入：文件名
// 输出：data/<文件名> 的相对路径
// 预期：目录不存在时自动创建，避免运行时文件散落在项目根目录；
// 安卓启动时把工作目录切到 app 私有目录，相对路径随之落到沙盒里

package datadir

import (
	"fmt"
	"os"
	"path/filepath"
)

// Dir 是运行时文件目录名。
const Dir = "data"

// Path 返回 data 目录下的路径并确保目录存在。
//
// 输入：name 文件名或子路径
// 输出：相对路径；目录创建失败时仍返回路径，由使用方报错
// 预期：建目录失败不静默吞掉——此时文件日志可能还没起来，
// 直接写 stderr 提示，避免错误点远离根因
func Path(name string) string {
	p := filepath.Join(Dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", p, err)
	}
	return p
}
