package telegramstore

// 并发设置回归：设置页保存会改下载/上传并发，此时可能正有传输在跑。

import (
	"sync"
	"testing"
)

// TestParallelSettingsConcurrentRead 运行期改并发与读取必须并发安全。
//
// 输入：200 组「写设置 + 读协程数」并发
// 输出：无数据竞争（配合 -race 跑）
// 预期：读到的值始终落在合法区间
func TestParallelSettingsConcurrentRead(t *testing.T) {
	st := New(&fakeAPI{}, 42)
	var wg sync.WaitGroup
	for i := range 200 {
		wg.Go(func() {
			st.SetParallel(i%40 + 1)
			st.SetUploadParallel(i%20 + 1)
		})
		wg.Go(func() {
			if n := st.threads(); n <= 0 || n > 32 {
				t.Errorf("threads 越界: %d", n)
			}
			if n := st.workers(); n <= 0 || n > 16 {
				t.Errorf("workers 越界: %d", n)
			}
		})
	}
	wg.Wait()
}
