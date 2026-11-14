package config

// 向配置文件追加卷条目。

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/natefinch/atomic"

	"mytgfs/internal/i18n"
)

// ErrVolumeExists 表示配置里已经有同名卷（忽略大小写）。
var ErrVolumeExists = errors.New(i18n.T("err.sentinel_volume_exists"))

// AppendVolume 在配置文件末尾追加一个 [[volumes]] 条目。
//
// 输入：path 配置文件；v 卷（Name 必填，ChatID 为频道 id）
// 输出：error；同名卷返回 ErrVolumeExists
// 预期：保留原有内容与注释，只在末尾追加；文件不存在时新建；
//
//	写入走临时文件 + rename，不会出现半截配置
func AppendVolume(path string, v Volume) error {
	name := strings.TrimSpace(v.Name)
	if name == "" {
		return errors.New(i18n.T("err.volume_name_empty"))
	}
	if strings.ContainsAny(name, "\r\n") {
		return errors.New(i18n.T("err.volume_name_newline"))
	}

	existing, err := Load(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, old := range existing.Volumes {
		if strings.EqualFold(old.Name, name) {
			return ErrVolumeExists
		}
	}

	old, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err == nil && len(old) > 0 {
		// 配置文件语法有问题时 Load 已经报错；这里只处理换行收尾
		if !bytes.HasSuffix(old, []byte("\n")) {
			old = append(old, '\n')
		}
	}

	var b bytes.Buffer
	b.Write(old)
	b.WriteString("\n[[volumes]]\n")
	fmt.Fprintf(&b, "name = %s\n", tomlString(name))
	fmt.Fprintf(&b, "chat_id = %d\n", v.ChatID)
	b.WriteString("enabled = true\n")

	return atomic.WriteFile(path, bytes.NewReader(b.Bytes()))
}

// ErrVolumeNotFound 表示配置里没有这个卷。
var ErrVolumeNotFound = errors.New(i18n.T("err.sentinel_volume_not_found"))

// RemoveVolume 从配置文件里删除指定名字的 [[volumes]] 整段。
//
// 输入：path 配置文件；name 卷名（忽略大小写）
// 输出：error；没有同名卷返回 ErrVolumeNotFound
// 预期：按行切段处理，保留其它段落与注释原样；写入走临时文件 + rename
func RemoveVolume(path, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New(i18n.T("err.volume_name_empty"))
	}

	old, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(string(old), "\n")

	var out []string
	removed := false
	for i := 0; i < len(lines); {
		if strings.TrimSpace(lines[i]) != "[[volumes]]" {
			out = append(out, lines[i])
			i++
			continue
		}
		// 找到本段结尾：下一个以 [ 开头的行
		j := i + 1
		for j < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[j]), "[") {
			j++
		}
		if volumeName(lines[i:j]) == "" || !strings.EqualFold(volumeName(lines[i:j]), name) {
			out = append(out, lines[i:j]...)
			i = j
			continue
		}
		// 命中：连同紧随其后的空行一起丢掉，避免留下连续空行
		k := j
		for k < len(lines) && strings.TrimSpace(lines[k]) == "" {
			k++
		}
		removed = true
		i = k
	}
	if !removed {
		return ErrVolumeNotFound
	}
	return atomic.WriteFile(path, strings.NewReader(strings.Join(out, "\n")))
}

// volumeName 从 [[volumes]] 段落里取 name 值。
//
// 输入：block 段落的所有行（含 [[volumes]] 头）
// 输出：去掉引号的卷名；没有 name 键时返回空串
// 预期：只认首个 name = "..." 行，支持单双引号
func volumeName(block []string) string {
	for _, line := range block {
		t := strings.TrimSpace(line)
		if !strings.HasPrefix(t, "name") {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(t, "name"))
		if !strings.HasPrefix(rest, "=") {
			continue
		}
		v := strings.TrimSpace(strings.TrimPrefix(rest, "="))
		v = strings.Trim(v, `"'`)
		return v
	}
	return ""
}

// tomlString 输出 TOML 基本字符串。
//
// 输入：s 任意文本（不含换行）
// 输出：带双引号的转义字符串
// 预期：只转义反斜杠与双引号，中文等 UTF-8 原样保留
func tomlString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}
