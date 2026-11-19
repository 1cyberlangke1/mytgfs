package app_test

import (
	"sync"
	"testing"

	"mytgfs/internal/app"
)

// 回归背景：删卷曾只改配置文件、不摘运行时列表，导致面板列卷与文件操作各说各话。
// VolumeRegistry 是列卷/开卷/删卷的唯一账本：Delete 必须立刻影响 Names/Get。

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

// 核心回归：Delete 之后列表与查找必须立刻少一个，不存在半删除状态
func TestRegistryDeleteRemovesImmediately(t *testing.T) {
	r := app.NewVolumeRegistry[*int]()
	v := 1
	r.Put("demo", &v)

	if !r.Delete("demo") {
		t.Fatal("Delete 存在的卷应返回 true")
	}
	if _, ok := r.Get("demo"); ok {
		t.Fatal("Delete 后 Get 仍能找到，列表未同步摘除")
	}
	if names := r.Names(); len(names) != 0 {
		t.Fatalf("Delete 后 Names() = %v; want 空", names)
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
			r.Delete("vol")
			_ = r.Names()
			_ = r.Snapshot()
			_, _ = r.Get("vol")
		}()
	}
	wg.Wait()
}
