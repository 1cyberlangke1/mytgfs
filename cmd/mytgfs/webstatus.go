package main

// `mytgfs web` 模式下的状态负载。
//
// 这个模式没有常驻挂载进程，但索引已经打开，足以给出真实的卷统计与上传队列；
// 面板因此不会一直显示「没有正在运行的实例」。

import (
	"context"

	"mytgfs/internal/config"
	"mytgfs/internal/dav"
	"mytgfs/internal/i18n"
	"mytgfs/internal/queue"
)

// webStatus 汇总各卷的条目数、占用量与排队中的上传。
//
// 输入：ctx；cfg 配置（决定卷顺序）；vols 已打开的卷
// 输出：可直接 JSON 序列化的状态负载
// 预期：
//   - 单个卷读取失败只在该卷上带 error，不影响其它卷
//   - 队列名加卷名前缀，面板能看出是哪个卷在上传
func webStatus(ctx context.Context, cfg config.Config, vols map[string]*webVolume) any {
	list := make([]map[string]any, 0, len(vols))
	var queued []queue.Item
	var xfers []dav.Transfer
	for _, v := range cfg.Volumes {
		wv, ok := vols[v.Name]
		if !ok {
			continue
		}
		children, err := wv.idx.Children(ctx, 0)
		if err != nil {
			list = append(list, map[string]any{"name": v.Name, "error": err.Error()})
			continue
		}
		used, _ := wv.idx.Used(ctx)
		list = append(list, map[string]any{
			"name": v.Name, "entries": len(children), "used": used,
		})
		if wv.davfs == nil {
			continue
		}
		for _, t := range wv.davfs.Transfers() {
			t.Path = v.Name + ":" + t.Path
			xfers = append(xfers, t)
		}
		q := wv.davfs.Queue()
		if q == nil {
			continue
		}
		for _, it := range q.Snapshot() {
			it.Name = v.Name + ":" + it.Name
			queued = append(queued, it)
		}
	}
	return map[string]any{
		"state":      i18n.T("web.state_local"),
		"state_code": "local",
		"auth":       "ok",
		"volumes":    list,
		"transfers":  xfers,
		"queue":      queued,
	}
}
