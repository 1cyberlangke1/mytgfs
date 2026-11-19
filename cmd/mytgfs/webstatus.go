//go:build !noweb

package main

// `mytgfs web` 模式下的状态负载。
//
// 这个模式没有常驻挂载进程，但索引已经打开，足以给出真实的卷统计与任务队列；
// 面板因此不会一直显示「没有正在运行的实例」。
// 每个卷怎么汇总统一由 app.VolumeFS.Snapshot 提供，这里只做拼装。

import (
	"context"

	"mytgfs/internal/queue"
	"mytgfs/internal/trayapp"
	"mytgfs/internal/vfs"
)

// webStatus 汇总各卷的条目数、占用量、下载进度与任务队列。
//
// 输入：ctx；vols 已打开的卷（按注册表顺序，面板建的新卷也在内）；
// code 是 Telegram 连接状态码（online/connecting/reconnecting/auth_lost）
// 输出：可直接 JSON 序列化的状态负载
// 预期：单个卷读取失败只在该卷上带 error，不影响其它卷；
// state_code 反映真实的 TG 连接状态，不能因为本地索引可用就报「已连接」
func webStatus(ctx context.Context, vols []*webVolume, code string) any {
	list := make([]map[string]any, 0, len(vols))
	var queued []queue.Item
	var xfers []vfs.Transfer
	for _, wv := range vols {
		if wv == nil || wv.vf == nil {
			continue
		}
		snap, err := wv.vf.Snapshot(ctx)
		if err != nil {
			list = append(list, map[string]any{"name": wv.vf.Name, "error": err.Error()})
			continue
		}
		list = append(list, map[string]any{
			"name": snap.Name, "entries": snap.Entries, "used": snap.Used,
		})
		xfers = append(xfers, snap.Transfers...)
		queued = append(queued, snap.Queue...)
	}
	return map[string]any{
		"state":      trayapp.StateText(code),
		"state_code": code,
		"auth":       "ok",
		"volumes":    list,
		"transfers":  xfers,
		"queue":      queued,
	}
}
