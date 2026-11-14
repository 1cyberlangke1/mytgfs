package push

// 面板推送契约：主题清单与事件名只在这里定义一次。
//
// 传输通道由 Hub 统一扇出：
//   - 桌面壳：Hub.Subscribe → Wails Event.Emit
//   - 网页版：Hub.Subscribe → SSE /api/events
//
// 新增主题只改 Topics，两端同时生效。

// 主题名。桌面壳与 SSE 使用完全相同的名字，前端按主题注册处理函数。
const (
	// TopicStatus 表示卷内文件、任务队列或连接状态发生变化
	TopicStatus = "status"
	// TopicConfig 表示配置被保存（可能改变并发数、下载目录等）
	TopicConfig = "config"
)

// Topics 是面板关心的全部主题。
//
// 输入：无
// 输出：订阅顺序固定的主题名切片
// 预期：新增主题只改这里，桌面壳与 SSE 同时生效
var Topics = []string{TopicStatus, TopicConfig}

// DesktopEvent 把主题翻译成桌面壳里的事件名。
//
// 输入：topic 主题名
// 输出：形如 mytgfs:status 的事件名
// 预期：前端 events.ts 的 desktopEvent 必须给出相同结果，两端字符串保持一致
func DesktopEvent(topic string) string { return "mytgfs:" + topic }
