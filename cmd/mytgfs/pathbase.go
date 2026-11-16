package main

// 全局基准目录：命令里的相对路径默认拼在 --dir / MYTGFS_DIR 之后。
//
// 输入：启动参数、环境变量
// 输出：volPath 用的基准目录 baseDir()
// 预期：一个进程只解析一次；不写全局参数时行为与以前完全一致（基准是 /）

import (
	"os"
	"path"
	"strings"
)

// globalDir 是 --dir 指定的卷内目录；空表示没写，退回 MYTGFS_DIR。
var globalDir string

// extractDirFlag 取出全局 --dir，返回其余参数。
//
// 输入：去掉 --json 之后的参数
// 输出：去掉 --dir 及其取值的参数
// 预期：支持 `--dir /x` 与 `--dir=/x` 两种写法；缺值按用法错误退出 2
func extractDirFlag(args []string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--dir":
			if i+1 >= len(args) {
				usage()
				os.Exit(2)
			}
			globalDir = args[i+1]
			i++
		case strings.HasPrefix(a, "--dir="):
			globalDir = strings.TrimPrefix(a, "--dir=")
		default:
			out = append(out, a)
		}
	}
	return out
}

// baseDir 返回相对路径的基准目录。
//
// 输入：无（读 --dir 与 MYTGFS_DIR）
// 输出：以 / 开头的卷内绝对路径，默认 "/"
// 预期：--dir 优先于环境变量；反斜杠归一成 /；空值视为卷根
func baseDir() string {
	p := strings.TrimSpace(globalDir)
	if p == "" {
		p = strings.TrimSpace(os.Getenv("MYTGFS_DIR"))
	}
	p = strings.ReplaceAll(p, `\`, "/")
	if p == "" {
		return "/"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return path.Clean(p)
}

// volPath 把命令行路径规范成卷内绝对路径。
//
// 输入：用户写的路径（可带 Windows 分隔符）
// 输出：以 / 开头的路径；相对写法拼在 baseDir() 之后
// 预期：反斜杠统一成正斜杠；绝对写法不受 --dir 影响
func volPath(p string) string {
	p = strings.ReplaceAll(p, `\`, "/")
	if p == "" {
		return baseDir()
	}
	if !strings.HasPrefix(p, "/") {
		p = path.Join(baseDir(), p)
	}
	return path.Clean(p)
}
