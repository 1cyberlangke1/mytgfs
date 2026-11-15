package app_test

import (
	"sync"
	"testing"

	"mytgfs/internal/app"
)

// 回归背景：删卷曾只改配置文件、不摘运行时列表，导致面板列卷与文件操作各说各话。
// VolumeRegistry 是列卷/开卷/删卷的唯一账本：Take 必须立刻影响 Names/Get。

func TestRegistryPutGetNamesOrder(t *testing.T) {
	r := app.NewVolumeRegistry[*int]()
	a, b := 1, 2
	r.Put("demo", &a)
	r.Put("demo2", &b)

	v, ok := r.Get("demo")
	if !ok || v != &a {
		t.Fatalf("Get(demo) = %v, %v; want %p, true", v, ok, &a)
	}
	names := r.Names()
	if len(names) != 2 || names[0] != "demo" || names[1] != "demo2" {
		t.Fatalf("Names() = %v; want 挂载顺序 [demo demo2]", names)
	}
}

// 核心回归：Take 之后列表与查找必须立刻少一个，不存在半删除状态
func TestRegistryTakeRemovesImmediately(t *testing.T) {
	r := app.NewVolumeRegistry[*int]()
	v := 1
	r.Put("demo", &v)

	got, ok := r.Take("demo")
	if !ok || got != &v {
		t.Fatalf("Take(demo) = %v, %v; want 句柄, true", got, ok)
	}
	if _, ok := r.Get("demo"); ok {
		t.Fatal("Take 后 Get 仍能找到，列表未同步摘除")
	}
	if names := r.Names(); len(names) != 0 {
		t.Fatalf("Take 后 Names() = %v; want 空", names)
	}
}

func TestRegistryTakeTwiceSecondFalse(t *testing.T) {
	r := app.NewVolumeRegistry[*int]()
	v := 1
	r.Put("demo", &v)
	if _, ok := r.Take("demo"); !ok {
		t.Fatal("第一次 Take 应成功")
	}
	if _, ok := r.Take("demo"); ok {
		t.Fatal("第二次 Take 应失败")
	}
}

// 删卷入口对配置名做大小写不敏感匹配时用 TakeFunc
func TestRegistryTakeFuncMatches(t *testing.T) {
	r := app.NewVolumeRegistry[*int]()
	v := 1
	r.Put("Demo2", &v)

	got, ok := r.TakeFunc(func(n string) bool { return equalFold(n, "demo2") })
	if !ok || got != &v {
		t.Fatalf("TakeFunc = %v, %v; want 句柄, true", got, ok)
	}
	if _, ok := r.Get("Demo2"); ok {
		t.Fatal("TakeFunc 后条目仍在")
	}
	if _, ok := r.TakeFunc(func(string) bool { return true }); ok {
		t.Fatal("TakeFunc 后重复摘取应失败")
	}
}

func TestRegistryDeleteNoopWhenMissing(t *testing.T) {
	r := app.NewVolumeRegistry[*int]()
	if r.Delete("nope") {
		t.Fatal("Delete 不存在的卷应返回 false")
	}
	v := 1
	r.Put("demo", &v)
	if !r.Delete("demo") {
		t.Fatal("Delete 存在的卷应返回 true")
	}
	if len(r.Names()) != 0 {
		t.Fatal("Delete 后列表应为空")
	}
}

func TestRegistrySnapshotMatchesNames(t *testing.T) {
	r := app.NewVolumeRegistry[*int]()
	a, b := 1, 2
	r.Put("demo", &a)
	r.Put("demo2", &b)

	snap := r.Snapshot()
	if len(snap) != 2 || snap[0] != &a || snap[1] != &b {
		t.Fatalf("Snapshot() 顺序错误: %v", snap)
	}
	if len(r.SnapshotMap()) != 2 {
		t.Fatalf("SnapshotMap() 长度错误: %v", r.SnapshotMap())
	}
}

// 列卷回调和删卷并发跑：race 检测下不出现数据竞争
func TestRegistryConcurrentAccess(t *testing.T) {
	r := app.NewVolumeRegistry[*int]()
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			n := i
			r.Put("vol", &n)
		}()
		go func() {
			defer wg.Done()
			r.TakeFunc(func(string) bool { return true })
			_ = r.Names()
			_ = r.Snapshot()
			_ = r.SnapshotMap()
			_, _ = r.Get("vol")
		}()
	}
	wg.Wait()
}

// equalFold 与 strings.EqualFold 同义；独立 helper 避免测试文件互相依赖
func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if ca >= 'A' && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if cb >= 'A' && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}
