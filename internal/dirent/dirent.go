package dirent

// 远端节点的编解码。
//
// 每条消息描述自己：名字、父节点消息号、类型（文件/目录），文件另带内容摘要。
// 名字存在节点自己身上，所以改名或移动只需编辑这一条消息，代价与子树大小无关。
// 父子关系靠消息号串起来：父消息号为 0 或指向不存在的消息时，节点挂在卷根 "/"。

import (
	"encoding/json"
	"errors"
	"strings"
)

// Version 是 body 格式版本；v1 是旧的「caption 里存整条路径」格式，本包不解析。
const Version = 2

// Body 类型字段取值。
const (
	// TypeFile 文件节点：消息里带 document。
	TypeFile = "f"
	// TypeDir 目录节点：消息是纯文本，没有 document。
	TypeDir = "d"
)

// CaptionLimit 是 Telegram 单条 caption 的字节上限。
const CaptionLimit = 1024

// MaxNameBytes 是节点名字的字节上限。
//
// body 只存名字、父消息号与摘要，名字限长后编码结果远小于 CaptionLimit，
// 元信息不需要拆到另一条消息里。
const MaxNameBytes = 255

// ErrBadBody 表示 body 不是本包认识的格式。
var ErrBadBody = errors.New("dirent: bad body")

// ErrNameTooLong 表示节点名字超过 MaxNameBytes。
var ErrNameTooLong = errors.New("dirent: name too long")

// Node 是一条远端节点消息描述的内容。
type Node struct {
	// Name 是节点在父目录里的名字，不含分隔符。
	Name string
	// Parent 是父目录的消息号；0 表示挂在卷根。
	Parent int
	// Dir 表示这是目录节点；false 表示文件节点。
	Dir bool
	// Sha 是文件内容的 sha256（小写十六进制），目录恒为空。
	Sha string
}

// body 是节点消息的 JSON 结构。
type body struct {
	Version int    `json:"v"`
	Type    string `json:"t"`
	Name    string `json:"n"`
	Parent  int    `json:"p,omitempty"`
	Sha     string `json:"s,omitempty"`
}

// validate 检查名字与父消息号是否可落到路径上。
//
// 输入：无（方法接收者）
// 输出：合法返回 nil；非法名字返回 ErrBadBody 或 ErrNameTooLong
// 预期：拒绝空名、"."、".."、含分隔符/NUL 的名字，以及超过 MaxNameBytes 的名字
func (n Node) validate() error {
	if n.Name == "" || n.Name == "." || n.Name == ".." {
		return ErrBadBody
	}
	if strings.ContainsAny(n.Name, "/\\\x00") {
		return ErrBadBody
	}
	if len(n.Name) > MaxNameBytes {
		return ErrNameTooLong
	}
	if n.Parent < 0 {
		return ErrBadBody
	}
	if n.Dir && n.Sha != "" {
		return ErrBadBody
	}
	return nil
}

// Encode 生成节点消息的 body。
//
// 输入：n 节点描述
// 输出：JSON 字符串；error
// 预期：名字非法或超长时返回错误；Parse 可原样取回
func Encode(n Node) (string, error) {
	if err := n.validate(); err != nil {
		return "", err
	}
	kind := TypeFile
	if n.Dir {
		kind = TypeDir
	}
	b, err := json.Marshal(body{Version: Version, Type: kind, Name: n.Name, Parent: n.Parent, Sha: n.Sha})
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Parse 解析节点消息的 body。
//
// 输入：body 字符串
// 输出：节点描述；ok=false 表示不是本包格式（含旧版带 path 的 caption）
func Parse(s string) (Node, bool) {
	var b body
	if err := json.Unmarshal([]byte(s), &b); err != nil {
		return Node{}, false
	}
	if b.Version != Version {
		return Node{}, false
	}
	if b.Type != TypeFile && b.Type != TypeDir {
		return Node{}, false
	}
	n := Node{Name: b.Name, Parent: b.Parent, Dir: b.Type == TypeDir, Sha: b.Sha}
	if err := n.validate(); err != nil {
		return Node{}, false
	}
	return n, true
}
