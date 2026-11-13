package dirent

// 远端目录项的编解码。
//
// 远端每个节点一条消息：文件消息持有 document，目录消息持有「名字 → 节点消息号」表。
// 名字只存在父目录里，所以改目录名只需改父目录那一条消息。

import (
	"bytes"
	"encoding/json"
	"errors"
)

// Version 是 body 格式版本；v1 是旧的「caption 里存整条路径」格式，本包不解析。
const Version = 2

// Body 类型字段取值。
const (
	// TypeFile 文件节点：消息里带 document。
	TypeFile = "f"
	// TypeDir 目录节点：消息里带目录项表。
	TypeDir = "d"
	// TypeChunk 目录项表的续块，跟在前一条后面。
	TypeChunk = "c"
)

// 分块阈值。
const (
	// CaptionLimit 是 Telegram caption 上限（1024）；留余量后超过阈值就走 ref 消息。
	CaptionLimit = 1024
	// RefThreshold 是内联 body 的字节上限：超过就改成 {"ref":id}。
	RefThreshold = 900
	// TextLimit 是 Telegram 文本消息上限（4096）；续块按阈值切。
	TextLimit = 4096
	// ChunkThreshold 是单条目录消息能携带的目录项 JSON 字节上限。
	ChunkThreshold = 3000
)

// ErrBadBody 表示 body 不是本包认识的格式。
var ErrBadBody = errors.New("dirent: bad body")

// Entry 是一个目录项：名字、类型、以及节点自己的消息号。
type Entry struct {
	Name  string `json:"-"`
	Dir   bool   `json:"-"`
	MsgID int    `json:"-"`
}

// kind 返回 JSON 数组里的类型字段。
func (e Entry) kind() string {
	if e.Dir {
		return TypeDir
	}
	return TypeFile
}

// MarshalJSON 编码成紧凑的三元组 [名字, "f"|"d", 消息号]，省 caption 空间。
func (e Entry) MarshalJSON() ([]byte, error) {
	return json.Marshal([3]any{e.Name, e.kind(), e.MsgID})
}

// UnmarshalJSON 解析三元组。
//
// 输入：JSON 数组
// 输出：填充 Entry；形状不对返回 ErrBadBody
func (e *Entry) UnmarshalJSON(b []byte) error {
	var raw []json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return ErrBadBody
	}
	if len(raw) != 3 {
		return ErrBadBody
	}
	var name, kind string
	var msgID int
	if err := json.Unmarshal(raw[0], &name); err != nil {
		return ErrBadBody
	}
	if err := json.Unmarshal(raw[1], &kind); err != nil {
		return ErrBadBody
	}
	if err := json.Unmarshal(raw[2], &msgID); err != nil {
		return ErrBadBody
	}
	if name == "" || msgID <= 0 {
		return ErrBadBody
	}
	if kind != TypeFile && kind != TypeDir {
		return ErrBadBody
	}
	e.Name = name
	e.Dir = kind == TypeDir
	e.MsgID = msgID
	return nil
}

// fileBody 是文件消息的 caption 结构。
type fileBody struct {
	Version int    `json:"v"`
	Type    string `json:"t"`
	Sha256  string `json:"s,omitempty"`
}

// dirBody 是目录消息（含续块）的结构。
type dirBody struct {
	Version int     `json:"v"`
	Type    string  `json:"t"`
	Next    int     `json:"n,omitempty"`
	Entries []Entry `json:"e"`
}

// refBody 是「正文过长，内容在另一条消息里」的占位结构。
type refBody struct {
	Ref int `json:"ref"`
}

// EncodeFile 生成文件消息的 caption。
//
// 输入：sha 内容摘要（小写十六进制，可为空）
// 输出：JSON 字符串
// 预期：sha 为空时不写 s 字段；ParseFile 可原样取回
func EncodeFile(sha string) (string, error) {
	b, err := json.Marshal(fileBody{Version: Version, Type: TypeFile, Sha256: sha})
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// ParseFile 解析文件消息的 caption。
//
// 输入：caption 字符串
// 输出：内容摘要（可能为空）、是否合法
// 预期：非本包格式（含旧版带 path 的 caption）返回 ok=false
func ParseFile(s string) (sha string, ok bool) {
	var b fileBody
	if err := json.Unmarshal([]byte(s), &b); err != nil {
		return "", false
	}
	if b.Version != Version || b.Type != TypeFile {
		return "", false
	}
	return b.Sha256, true
}

// EncodeDir 生成目录消息的 body。
//
// 输入：entries 该目录的全部目录项；next 续块消息号（0 表示链尾）
// 输出：JSON 字符串
// 预期：ParseDir 取回同样的 entries 与 next
func EncodeDir(entries []Entry, next int) (string, error) {
	return encodeEntries(TypeDir, entries, next)
}

// EncodeChunk 生成续块的 body。
//
// 输入：entries 续块携带的目录项；next 下一条续块消息号（0 表示链尾）
// 输出：JSON 字符串
func EncodeChunk(entries []Entry, next int) (string, error) {
	return encodeEntries(TypeChunk, entries, next)
}

// encodeEntries 是 EncodeDir / EncodeChunk 的共用实现。
func encodeEntries(kind string, entries []Entry, next int) (string, error) {
	if entries == nil {
		entries = []Entry{}
	}
	b, err := json.Marshal(dirBody{Version: Version, Type: kind, Next: next, Entries: entries})
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// ParseDir 解析目录消息或续块的 body。
//
// 输入：body 字符串
// 输出：kind（TypeDir 或 TypeChunk）、目录项、next
// 预期：版本或类型不符返回 err；ref 占位不是本函数处理范围
func ParseDir(s string) (kind string, entries []Entry, next int, err error) {
	var b dirBody
	if err := json.Unmarshal([]byte(s), &b); err != nil {
		return "", nil, 0, ErrBadBody
	}
	if b.Version != Version {
		return "", nil, 0, ErrBadBody
	}
	if b.Type != TypeDir && b.Type != TypeChunk {
		return "", nil, 0, ErrBadBody
	}
	return b.Type, b.Entries, b.Next, nil
}

// EncodeRef 生成正文替身：指到真正存放 body 的那条消息。
//
// 输入：id 存放 body 的消息号
// 输出：JSON 字符串
// 预期：与 IsRef 往返一致
func EncodeRef(id int) (string, error) {
	b, err := json.Marshal(refBody{Ref: id})
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// IsRef 判断 body 是否是替身，并取出真实消息号。
//
// 输入：body 字符串
// 输出：消息号；不是替身时 ok=false
func IsRef(s string) (id int, ok bool) {
	var b refBody
	if err := json.Unmarshal([]byte(s), &b); err != nil {
		return 0, false
	}
	if b.Ref <= 0 {
		return 0, false
	}
	return b.Ref, true
}

// NeedsRef 判断内联 body 是否已超过 caption 安全阈值。
//
// 输入：body 编码后的字符串
// 输出：true 表示要改成 {"ref":id}
func NeedsRef(body string) bool {
	return len(body) > RefThreshold
}

// SplitEntries 按字节预算把目录项切成多块。
//
// 输入：entries 目录项；maxBytes 单块目录项 JSON 的字节上限（<=0 用 ChunkThreshold）
// 输出：至少一块（无目录项时返回一个空块）；单条目录项超预算时独占一块
// 预期：块内 entries 按原顺序，拼接后与输入完全一致
func SplitEntries(entries []Entry, maxBytes int) ([][]Entry, error) {
	if maxBytes <= 0 {
		maxBytes = ChunkThreshold
	}
	if len(entries) == 0 {
		return [][]Entry{{}}, nil
	}
	out := make([][]Entry, 0, 4)
	cur := make([]Entry, 0, len(entries))
	size := 2 // 外层 '[' ']'
	for _, e := range entries {
		b, err := e.MarshalJSON()
		if err != nil {
			return nil, err
		}
		add := len(b)
		if len(cur) > 0 {
			add++ // 逗号
		}
		if len(cur) > 0 && size+add > maxBytes {
			out = append(out, cur)
			cur = make([]Entry, 0, 4)
			size = 2
			add = len(b)
		}
		cur = append(cur, e)
		size += add
	}
	out = append(out, cur)
	return out, nil
}

// Equal 比较两个目录项是否同一节点（名字与类型相同，或消息号相同）。
//
// 输入：a、b
// 输出：相等返回 true
// 预期：用于 rename 时判断条目是否变化
func Equal(a, b Entry) bool {
	return a.MsgID == b.MsgID && a.Name == b.Name && a.Dir == b.Dir
}

// Find 在目录项里按名字查找，名字不区分大小写不适用：远端是大小写敏感的。
//
// 输入：entries；name
// 输出：命中项；ok
func Find(entries []Entry, name string) (Entry, bool) {
	for _, e := range entries {
		if e.Name == name {
			return e, true
		}
	}
	return Entry{}, false
}

// Merge 把 add 里的条目并入 base（按名字覆盖），返回新切片；base 顺序保留。
//
// 输入：base 已有条目；add 待写入条目
// 输出：合并结果
// 预期：add 中名字已存在则原位替换，否则追加到尾
func Merge(base, add []Entry) []Entry {
	out := make([]Entry, len(base), len(base)+len(add))
	copy(out, base)
	for _, a := range add {
		replaced := false
		for i := range out {
			if out[i].Name == a.Name {
				out[i] = a
				replaced = true
				break
			}
		}
		if !replaced {
			out = append(out, a)
		}
	}
	return out
}

// Drop 从 entries 里删掉指定名字，返回新切片与是否删掉。
//
// 输入：entries；name
// 输出：过滤后的切片；deleted
func Drop(entries []Entry, name string) ([]Entry, bool) {
	out := make([]Entry, 0, len(entries))
	deleted := false
	for _, e := range entries {
		if e.Name == name {
			deleted = true
			continue
		}
		out = append(out, e)
	}
	return out, deleted
}

// Rename 把 entries 里 oldName 改成 newName（节点号不变）。
//
// 输入：entries；oldName；newName
// 输出：改后的切片；ok 表示找到并改名
// 预期：newName 已存在时不改（由调用方判定冲突）
func Rename(entries []Entry, oldName, newName string) ([]Entry, bool) {
	if _, exists := Find(entries, newName); exists {
		return entries, false
	}
	out := make([]Entry, len(entries))
	copy(out, entries)
	for i := range out {
		if out[i].Name == oldName {
			out[i].Name = newName
			return out, true
		}
	}
	return entries, false
}

// BodyOf 判断 body 属于哪种节点；ref 占位无法判断，返回空串。
//
// 输入：body 字符串
// 输出：TypeFile / TypeDir / TypeChunk / 空串
func BodyOf(s string) string {
	trimmed := bytes.TrimSpace([]byte(s))
	if len(trimmed) == 0 {
		return ""
	}
	var probe struct {
		Version int    `json:"v"`
		Type    string `json:"t"`
	}
	if err := json.Unmarshal(trimmed, &probe); err != nil {
		return ""
	}
	if probe.Version != Version {
		return ""
	}
	switch probe.Type {
	case TypeFile, TypeDir, TypeChunk:
		return probe.Type
	}
	return ""
}
