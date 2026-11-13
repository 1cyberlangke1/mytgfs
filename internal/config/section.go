package config

// 改写配置文件里某个 [section] 的键值。
//
// 只替换受管的键：段内其它键、注释、顺序保持原样；缺的键补在段末；
// 段不存在时追加到文件末尾。写入走临时文件 + rename，不会出现半截配置。

import (
	"bytes"
	"os"
	"strings"

	"github.com/natefinch/atomic"
)

// rewriteSection 更新 path 里 [name] 段。
//
// 输入：path 配置文件；name 段名（不含方括号）；kv 受管键值行（形如 "key = value"）；
// drop 需要整行删除的键
// 输出：error
// 预期：
//   - 段存在时受管键原位替换，缺的补在段末，drop 里的键整行删除
//   - 段内其它内容与注释逐字节保留
//   - 段不存在时按 kv 顺序追加新段
func rewriteSection(path, name string, kv []string, drop []string) error {
	old, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	lines := strings.Split(string(old), "\n")
	header := "[" + name + "]"

	start := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == header {
			start = i
			break
		}
	}
	if start < 0 {
		if len(old) > 0 && !bytes.HasSuffix(old, []byte("\n")) {
			lines = append(lines, "")
		}
		lines = append(lines, header)
		lines = append(lines, kv...)
		return atomic.WriteFile(path, strings.NewReader(strings.Join(lines, "\n")))
	}

	// 段尾：下一个以 [ 开头的行
	end := start + 1
	for end < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[end]), "[") {
		end++
	}
	body := rewriteKeys(lines[start+1:end], kv, drop)
	out := make([]string, 0, len(lines)+len(kv))
	out = append(out, lines[:start+1]...)
	out = append(out, body...)
	out = append(out, lines[end:]...)
	return atomic.WriteFile(path, strings.NewReader(strings.Join(out, "\n")))
}

// rewriteKeys 替换段内的受管键。
//
// 输入：body 段内所有行（不含段头）；kv 受管键值行；drop 需删除的键
// 输出：重写后的行
// 预期：受管键原位替换；缺的按 kv 顺序补在段末；其余行原样保留
func rewriteKeys(body, kv, drop []string) []string {
	want := make(map[string]string, len(kv))
	order := make([]string, 0, len(kv))
	for _, line := range kv {
		k, v := splitKeyValue(line)
		if k == "" {
			continue
		}
		want[k] = v
		order = append(order, k)
	}
	managed := make(map[string]bool, len(drop))
	for _, k := range drop {
		managed[k] = true
	}

	seen := make(map[string]bool, len(kv))
	out := make([]string, 0, len(body)+len(kv))
	for _, line := range body {
		key, _ := splitKeyValue(line)
		if repl, ok := want[key]; ok {
			out = append(out, key+" = "+repl)
			seen[key] = true
			continue
		}
		if key != "" && managed[key] {
			continue
		}
		out = append(out, line)
	}
	for _, key := range order {
		if seen[key] {
			continue
		}
		out = append(out, key+" = "+want[key])
	}
	return out
}

// splitKeyValue 拆出 "key = value" 的键与值。
//
// 输入：一行文本
// 输出：键（去空白）；值（原样，含引号）；不是键值行时键为空
// 预期：注释行、空行、段头一律当成无键
func splitKeyValue(line string) (key, value string) {
	t := strings.TrimSpace(line)
	if t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, "[") {
		return "", ""
	}
	key, value, ok := strings.Cut(t, "=")
	if !ok || key == "" {
		return "", ""
	}
	return strings.TrimSpace(key), strings.TrimSpace(value)
}

// boolLiteral 输出 TOML 布尔字面量。
func boolLiteral(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
