package version

// 构建信息：版本、提交号、构建时间与运行环境。
//
// 前三个变量由构建脚本用 -ldflags -X 注入；未注入时保持 dev/none，
// 排障时至少能看到运行环境与 Go 版本。

import (
	"runtime"
)

var (
	// Version 是语义化版本号，构建时注入。
	Version = "dev"
	// Commit 是提交短哈希，构建时注入。
	Commit = "none"
	// Date 是构建时间（RFC3339），构建时注入。
	Date = "unknown"
)

// Info 是版本信息快照。
type Info struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Date    string `json:"date"`
	Go      string `json:"go"`
	OS      string `json:"os"`
	Arch    string `json:"arch"`
}

// Get 返回当前构建信息。
//
// 输入：无
// 输出：Info
// 预期：纯读取全局变量，可在任意 goroutine 调用
func Get() Info {
	return Info{
		Version: Version,
		Commit:  Commit,
		Date:    Date,
		Go:      runtime.Version(),
		OS:      runtime.GOOS,
		Arch:    runtime.GOARCH,
	}
}

// String 返回单行可读版本串。
//
// 输入：无
// 输出：形如 mytgfs dev (commit none, built unknown, go1.26.1 windows/amd64)
func String() string {
	i := Get()
	return "mytgfs " + i.Version + " (commit " + i.Commit + ", built " + i.Date + ", " + i.Go + " " + i.OS + "/" + i.Arch + ")"
}
