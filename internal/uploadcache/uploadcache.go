package uploadcache

// 上传断点的磁盘存储。
//
// 每个断点一个 JSON 文件：<dir>/<key>.json，key 用内容 sha256。
// 写入走临时文件 + rename，进程被杀时不会留下半个 JSON。

import (
	"cmp"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/natefinch/atomic"

	"mytgfs/internal/telegramstore"
)

// Store 是文件系统上的断点存储。
type Store struct {
	dir string
}

// New 创建断点存储。
//
// 输入：dir 存放目录，不存在时按需创建
// 输出：*Store
// 预期：目录创建失败不在这里报错，真正写盘时再暴露
func New(dir string) *Store { return &Store{dir: dir} }

// Sweep 清理过期断点文件。
//
// 输入：dir 断点目录；maxAge 文件年龄上限（Telegram 服务端临时区约 24h 过期，
// 超过这个时间的断点即使还在也续不上，留着只会堆积）
// 输出：删除的文件数；目录不存在返回 0
// 预期：进程启动时调用一次；只删 .json 文件，不动其它内容
func Sweep(dir string, maxAge time.Duration) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	cutoff := time.Now().Add(-maxAge)
	n := 0
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		info, err := e.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		if os.Remove(filepath.Join(dir, e.Name())) == nil {
			n++
		}
	}
	return n
}

// Load 读取断点。
//
// 输入：key 断点键
// 输出：断点；文件不存在时返回 (nil, nil)
// 预期：内容损坏时返回 (nil, nil)，当作没有断点重传，不阻塞上传
func (s *Store) Load(key string) (*telegramstore.UploadResume, error) {
	b, err := os.ReadFile(s.path(key))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var r telegramstore.UploadResume
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, nil
	}
	return &r, nil
}

// Save 写入断点。
//
// 输入：key 断点键；r 断点
// 输出：error
// 预期：先建目录再原子替换；key 中的路径分隔符会被替换掉
func (s *Store) Save(key string, r *telegramstore.UploadResume) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return atomic.WriteFile(s.path(key), strings.NewReader(string(b)))
}

// Clear 删除断点。
//
// 输入：key 断点键
// 输出：error
// 预期：文件不存在不算错误
func (s *Store) Clear(key string) error {
	err := os.Remove(s.path(key))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// path 把 key 映射到磁盘路径。
//
// 输入：key
// 输出：合法文件名
// 预期：只保留 key 的安全性无关字符替换，避免出现子目录
func (s *Store) path(key string) string {
	safe := strings.NewReplacer("/", "_", "\\", "_", ":", "_", "..", "_").Replace(key)
	safe = cmp.Or(safe, "unnamed")
	return filepath.Join(s.dir, safe+".json")
}
