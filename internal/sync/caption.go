package sync

import "encoding/json"

// caption 是文件消息 caption 的结构。
//
// 输入/输出：由 EncodeCaptionHash 序列化、ParseFile 反序列化
// 预期：sha256 省略时与旧版本 caption 完全一致（只有 path 字段）
type caption struct {
	Path   string `json:"path"`
	Sha256 string `json:"sha256,omitempty"`
}

// EncodeCaption 生成只有路径的 caption（保留给不需要摘要的调用方）。
//
// 输入：path 虚拟路径，如 "/docs/a.txt"
// 输出：JSON 字符串
// 预期：等价于 EncodeCaptionHash(path, "")
func EncodeCaption(path string) string {
	return EncodeCaptionHash(path, "")
}

// EncodeCaptionHash 生成带内容摘要的 caption。
//
// 输入：path 虚拟路径；sha 内容摘要（小写十六进制，可为空）
// 输出：JSON 字符串
// 预期：sha 为空时不含 sha256 字段；与 ParseFile 往返一致
func EncodeCaptionHash(path, sha string) string {
	b, _ := json.Marshal(caption{Path: path, Sha256: sha})
	return string(b)
}

// ParseCaption 解析 caption 里的 path。
//
// 输入：caption 字符串
// 输出：path；非法或空时 ok=false
// 预期：非 JSON、缺 path、空白 path 都返回 false
func ParseCaption(s string) (string, bool) {
	p, _, ok := ParseFile(s)
	return p, ok
}

// ParseFile 解析 caption 里的路径与内容摘要。
//
// 输入：caption 字符串
// 输出：path、sha256（可能为空）、是否合法
// 预期：旧版本 caption 没有 sha256 时返回空摘要，不影响 path 解析
func ParseFile(s string) (path, sha string, ok bool) {
	var c caption
	if err := json.Unmarshal([]byte(s), &c); err != nil {
		return "", "", false
	}
	if c.Path == "" {
		return "", "", false
	}
	return c.Path, c.Sha256, true
}
