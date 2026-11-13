package config

// 改写配置文件里的 [webdav] 段。
//
// 只动三个键（enabled / addr / volume），其它段落、注释、未知键原样保留。

import "strings"

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
	var drop []string
	if strings.TrimSpace(w.Volume) == "" {
		drop = []string{"volume"}
	}
	return rewriteSection(path, "webdav", webdavKV(w), drop)
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
