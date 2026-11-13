package netmap

// 清理指向本机 WebDAV 服务的 Windows 网络映射。
//
// 目的：服务退出后盘符会变成红叉，下次启动还会冲突；
// 因此启停服务时都要主动 net use /delete。
// 非 Windows 平台没有这个概念，直接跳过。

import (
	"os/exec"
	"runtime"
	"strings"
)

// Cleanup 删除指向 addr 的网络映射。
//
// 输入：addr 监听地址（如 127.0.0.1:8080）
// 输出：无
// 预期：
//   - 只处理命令行里包含该地址的映射，其它盘符不动
//   - net use 把 host:port 显示成 host@port，两种写法都匹配
//   - 非 Windows 或 net use 不可用时静默返回
func Cleanup(addr string) {
	if runtime.GOOS != "windows" || addr == "" {
		return
	}
	out, err := exec.Command("net", "use").Output()
	if err != nil {
		return
	}
	patterns := []string{addr, strings.Replace(addr, ":", "@", 1)}
	for line := range strings.SplitSeq(string(out), "\n") {
		matched := false
		for _, p := range patterns {
			if p != "" && strings.Contains(line, p) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		// 盘符是形如 "Y:" 的两字符字段
		for f := range strings.FieldsSeq(line) {
			if len(f) == 2 && strings.HasSuffix(f, ":") {
				_ = exec.Command("net", "use", f, "/delete", "/y").Run()
			}
		}
	}
}
