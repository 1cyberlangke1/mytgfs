package sync

import "encoding/json"

// caption 是文件消息 caption 的结构。
type caption struct {
	Path string `json:"path"`
}

// EncodeCaption 生成文件消息的 caption。
//
// 输入：path 虚拟路径，如 "/docs/a.txt"
// 输出：JSON 字符串
// 预期：与 ParseCaption 往返一致
func EncodeCaption(path string) string {
	b, _ := json.Marshal(caption{Path: path})
	return string(b)
}

// ParseCaption 解析 caption 里的 path。
//
// 输入：caption 字符串
// 输出：path；非法或空时 ok=false
// 预期：非 JSON、缺 path、空白 path 都返回 false
func ParseCaption(s string) (string, bool) {
	var c caption
	if err := json.Unmarshal([]byte(s), &c); err != nil {
		return "", false
	}
	if c.Path == "" {
		return "", false
	}
	return c.Path, true
}
