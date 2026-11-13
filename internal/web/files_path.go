package web

// 面板文件管理的路径处理：上传落点、相对路径规范化、文件名校验、错误翻人话。

import (
	"errors"
	"os"
	"path"
	"strings"

	"mytgfs/internal/fm"
	"mytgfs/internal/i18n"
)

// uploadTarget 算出上传落点。
//
// 输入：dir 当前目录；rel 浏览器给的相对路径（可空）；name 文件名
// 输出：卷内绝对路径；error
// 预期：
//   - rel 必须带文件名（以 / 结尾视为目录路径，直接拒绝）
//   - rel 里的 .. 段一律拒绝，防止写到卷外或其它目录
func uploadTarget(dir, rel, name string) (string, error) {
	rel = strings.TrimSpace(rel)
	if rel == "" {
		n := safeName(name)
		if n == "" {
			return "", fm.ErrBadPath
		}
		return path.Join(dir, n), nil
	}
	if strings.HasSuffix(rel, "/") || strings.HasSuffix(rel, `\`) {
		return "", fm.ErrBadPath
	}
	n, ok := cleanRelPath(rel)
	if !ok {
		return "", fm.ErrBadPath
	}
	return path.Join(dir, n), nil
}

// cleanRelPath 规范化浏览器给的相对路径。
//
// 输入：rel 形如 "sub/dir/a.txt"，允许反斜杠
// 输出：干净相对路径；含 .. 或为空时 ok=false
// 预期：保留子目录结构，但绝不包含绝对路径成分
func cleanRelPath(rel string) (string, bool) {
	rel = strings.ReplaceAll(rel, `\`, "/")
	rel = strings.TrimPrefix(rel, "/")
	var segs []string
	for seg := range strings.SplitSeq(rel, "/") {
		switch seg {
		case "", ".":
			continue
		case "..":
			return "", false
		}
		segs = append(segs, seg)
	}
	if len(segs) == 0 {
		return "", false
	}
	return strings.Join(segs, "/"), true
}

// normalizeDir 规范化目录参数。
func normalizeDir(dir string) string {
	dir = strings.ReplaceAll(dir, `\`, "/")
	if dir == "" {
		return "/"
	}
	if !strings.HasPrefix(dir, "/") {
		dir = "/" + dir
	}
	return path.Clean(dir)
}

// safeName 校验用户给的文件名。
//
// 输入：原始名字
// 输出：去掉路径成分后合法的名字；非法返回空串
// 预期：不允许带目录分隔符或 ..，避免越出当前目录
func safeName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." {
		return ""
	}
	if strings.ContainsAny(name, `/\`) {
		return ""
	}
	return name
}

// fmError 把内部错误翻成人话。
func fmError(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, fm.ErrBadPath):
		return i18n.T("err.fm_bad_path")
	case errors.Is(err, fm.ErrExists):
		return i18n.T("err.fm_exists")
	case errors.Is(err, os.ErrNotExist):
		return i18n.T("err.fm_not_found")
	}
	return err.Error()
}
