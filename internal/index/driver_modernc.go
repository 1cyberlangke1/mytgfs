//go:build !android

package index

// 桌面与服务器的 SQLite 驱动：modernc.org/sqlite（C 源码转译成纯 Go）。
//
// 输入：无，只在 init 里向 database/sql 注册驱动
// 输出：驱动名 "sqlite"（见 driverName）
// 预期：非安卓平台都走这里；安卓换驱动的原因写在 driver_android.go
import _ "modernc.org/sqlite"

// driverName 是 database/sql 里的驱动注册名，两个平台的驱动名不同。
const driverName = "sqlite"

// dsnFor 把索引文件路径拼成 modernc 认识的 DSN。
//
// 输入：文件路径（可为相对路径，Windows 盘符原样保留）
// 输出：路径本身加两个 _pragma 查询参数
// 预期：modernc 直接按路径打开，参数部分由它自己解析
func dsnFor(path string) string {
	return path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
}
