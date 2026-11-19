package app

// VolumeRegistry 是运行中卷的唯一账本：列卷、按名查找、摘除共用同一份数据。
//
// 背景：删卷曾只改配置文件、不摘运行时列表，导致面板列卷（读列表）
// 与文件操作（按配置解析）各说各话的半删除状态。此后所有删卷路径
// 必须走 Take/TakeFunc：先摘除拿到句柄，再等资源关闭与删盘，
// 结构上保证「删完即列表同步」。

import (
	"sync"
)

// VolumeRegistry 泛型注册表，T 为卷的运行时句柄类型。
type VolumeRegistry[T any] struct {
	mu    sync.Mutex
	order []string
	vols  map[string]T
}

// NewVolumeRegistry 创建空注册表。
func NewVolumeRegistry[T any]() *VolumeRegistry[T] {
	return &VolumeRegistry[T]{vols: map[string]T{}}
}

// Put 登记一个卷；同名覆盖且保持原有顺序位置。
func (r *VolumeRegistry[T]) Put(name string, v T) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.vols[name]; !exists {
		r.order = append(r.order, name)
	}
	r.vols[name] = v
}

// Get 按名查找；摘除后返回 false。
func (r *VolumeRegistry[T]) Get(name string) (T, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.vols[name]
	return v, ok
}

// Delete 摘除并丢弃句柄；不存在时为无副作用的 no-op。
// 给挂载清理协程用：删卷路径可能已经摘走，重复调用必须无害。
func (r *VolumeRegistry[T]) Delete(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.vols[name]; !ok {
		return false
	}
	delete(r.vols, name)
	r.dropOrderLocked(name)
	return true
}

// Names 返回卷名，按挂载顺序。
func (r *VolumeRegistry[T]) Names() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.order...)
}

// Snapshot 返回卷句柄切片，按挂载顺序。
func (r *VolumeRegistry[T]) Snapshot() []T {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]T, 0, len(r.order))
	for _, name := range r.order {
		out = append(out, r.vols[name])
	}
	return out
}

// dropOrderLocked 从顺序表里摘除卷名；调用方必须已持有 mu。
func (r *VolumeRegistry[T]) dropOrderLocked(name string) {
	for i, n := range r.order {
		if n == name {
			r.order = append(r.order[:i], r.order[i+1:]...)
			return
		}
	}
}
