package config

// 向配置文件追加卷条目。

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"

	"mytgfs/internal/atomicfile"
)

// ErrVolumeExists 表示配置里已经有同名卷（忽略大小写）。
var ErrVolumeExists = errors.New("volume already exists")

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
		return fmt.Errorf("卷名不能为空")
	}
	if strings.ContainsAny(name, "\r\n") {
		return fmt.Errorf("卷名不能包含换行")
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
	if p := strings.TrimSpace(v.Point); p != "" {
		fmt.Fprintf(&b, "point = %s\n", tomlString(p))
	}
	b.WriteString("enabled = true\n")

	return atomicfile.WriteFile(path, b.Bytes(), 0o600)
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
