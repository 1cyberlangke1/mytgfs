package push

// 写操作的推送通道。
//
// 编译期约束：卷的构造函数、队列的构造函数都必须接收 Events。
// 没接推送就构造不出可写的卷 —— 忘记推送会变成编译错误，
// 而不是「改完没反应」这种只能靠人眼发现的静默故障。

import (
	"sync"
	"time"
)

// DefaultGap 是状态事件的最小推送间隔。
//
// 输入：无
// 输出：400ms
// 预期：上传进度按块触发，节流后前端每秒最多几次；尾沿仍保证最后一次送达
const DefaultGap = 400 * time.Millisecond

// Events 是写操作完成后通知前端的唯一入口。
//
// 输入：topic 主题名（用本包的 Topic* 常量）
// 输出：无
// 预期：生产实现是 *Hub；实现必须非阻塞；重复推送允许（前端只拉快照）
type Events interface {
	Publish(topic string)
}

// Recorder 是测试用 Events：按顺序记录收到的主题。
type Recorder struct {
	mu     sync.Mutex
	topics []string
}

// NewRecorder 创建记录器。
func NewRecorder() *Recorder { return &Recorder{} }

// Publish 记录一次推送。
//
// 输入：topic 主题名
// 输出：无
// 预期：并发安全；不做去重，调用几次就记几次
func (r *Recorder) Publish(topic string) {
	r.mu.Lock()
	r.topics = append(r.topics, topic)
	r.mu.Unlock()
}

// Topics 返回收到的主题副本。
func (r *Recorder) Topics() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.topics...)
}

// Count 返回某个主题被推送的次数。
func (r *Recorder) Count(topic string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, t := range r.topics {
		if t == topic {
			n++
		}
	}
	return n
}

// Reset 清空记录。
func (r *Recorder) Reset() {
	r.mu.Lock()
	r.topics = nil
	r.mu.Unlock()
}
