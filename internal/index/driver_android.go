//go:build android

package index

// 安卓的 SQLite 驱动：ncruces/go-sqlite3（SQLite 编成 WASM，跑在 wazero 里）。
//
// 为什么安卓不能继续用 modernc.org/sqlite：
// 它底下的 modernc.org/libc 是 musl 的 ccgo 转译，linux/amd64 那份生成代码
// 用原始 lstat 系统调用（SYS_lstat=6）解析数据库路径。Android 8 起给所有应用
// 装 seccomp 过滤器，白名单只认 bionic 真正会发的 newfstatat 系列，
// 因此第一次打开索引文件就被 SIGSYS 杀掉（进程存活约 10 秒后崩在开库处）。
// arm64 没有 lstat 这个系统调用，modernc 走的是 fstatat，所以只有 x86_64 中招；
// 模拟器与 x86 Chromebook 都属于这一档。
//
// ncruces 把文件读写交给 Go 标准库（wazero 的 WASI 实现），
// 标准库在 Android 上早就不用被过滤的旧系统调用。
//
// 输入：无，只在 init 里向 database/sql 注册驱动
// 输出：驱动名 "sqlite3"（见 driverName）
// 预期：安卓上打开索引不再触发 SIGSYS；库文件格式与桌面完全一致
import _ "github.com/ncruces/go-sqlite3/driver"

// driverName 是 database/sql 里的驱动注册名，两个平台的驱动名不同。
const driverName = "sqlite3"

// dsnFor 把索引文件路径拼成 ncruces 认识的 DSN。
//
// 输入：索引文件的绝对路径（安卓沙盒路径是 POSIX 形式，不带盘符）
// 输出："file:<path>?_pragma=..." 形式的 URI
// 预期：ncruces 只对 file: URI 解析查询参数，裸路径会把 ?_pragma=... 当成
//
//	文件名的一部分，进而报 unable to open database file
func dsnFor(path string) string {
	return "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
}
