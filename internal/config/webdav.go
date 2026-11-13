package config

// 改写配置文件里的 [webdav] 段。
//
// 只动三个键（enabled / addr / volume），其它段落、注释、未知键原样保留。

import (
	"bytes"
	"os"
	"strings"

	"github.com/natefinch/atomic"
)

// webdavKeys 是受管理的键，顺序即追加顺序。
var webdavKeys = []string{"enabled", "addr", "volume"}

// SetWebDAV 更新配置文件里的 [webdav] 段。
//
// 输入：path 配置文件路径；w 新的 WebDAV 设置
// 输出：error
// 预期：
//   - 段落存在时只改这三个键，段内注释与未知键保留
//   - Volume 为空时把 volume 键整行删掉（表示「用第一个启用的卷」）
//   - 段落不存在时追加到文件末尾
//   - 写入走临时文件 + rename，不会出现半截配置
func SetWebDAV(path string, w WebDAV) error {
	old, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	lines := strings.Split(string(old), "\n")

	start := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == "[webdav]" {
			start = i
			break
		}
	}

	if start < 0 {
		if len(old) > 0 && !bytes.HasSuffix(old, []byte("\n")) {
			lines = append(lines, "")
		}
		lines = append(lines, "[webdav]")
		lines = append(lines, webdavKV(w)...)
		return atomic.WriteFile(path, strings.NewReader(strings.Join(lines, "\n")))
	}

	// 找段尾：下一个以 [ 开头的行
	end := start + 1
	for end < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[end]), "[") {
		end++
	}
	body := rewriteWebDAVSection(lines[start+1:end], w)
	out := make([]string, 0, len(lines)+len(webdavKeys))
	out = append(out, lines[:start+1]...)
	out = append(out, body...)
	out = append(out, lines[end:]...)
	return atomic.WriteFile(path, strings.NewReader(strings.Join(out, "\n")))
}

// webdavKV 返回需要写入的键值行。
func webdavKV(w WebDAV) []string {
	kv := []string{
		"enabled = " + boolLiteral(w.Enabled),
		"addr = " + tomlString(w.ListenAddr()),
	}
	if v := strings.TrimSpace(w.Volume); v != "" {
		kv = append(kv, "volume = "+tomlString(v))
	}
	return kv
}

// rewriteWebDAVSection 重写段内键值。
//
// 输入：body 段内所有行（不含 [webdav] 头）；w 新设置
// 输出：重写后的行
// 预期：受管理的键原位替换；缺的键追加在末尾；volume 为空则删掉该行
func rewriteWebDAVSection(body []string, w WebDAV) []string {
	want := map[string]string{}
	for _, line := range webdavKV(w) {
		k, v := splitKeyValue(line)
		want[k] = v
	}

	seen := map[string]bool{}
	out := make([]string, 0, len(body)+len(webdavKeys))
	for _, line := range body {
		key, _ := splitKeyValue(line)
		if repl, ok := want[key]; ok {
			out = append(out, key+" = "+repl)
			seen[key] = true
			continue
		}
		if key == "volume" {
			// 显式清空
			continue
		}
		out = append(out, line)
	}
	for _, key := range webdavKeys {
		if seen[key] {
			continue
		}
		if repl, ok := want[key]; ok {
			out = append(out, key+" = "+repl)
		}
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
	i := strings.IndexByte(t, '=')
	if i <= 0 {
		return "", ""
	}
	return strings.TrimSpace(t[:i]), strings.TrimSpace(t[i+1:])
}

// boolLiteral 输出 TOML 布尔字面量。
func boolLiteral(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
