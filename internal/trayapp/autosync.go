package trayapp

// 托盘主管进程：挂载卷、自动增量同步、按需开关 Web 面板、托盘菜单。
//
// 这个包不依赖命令行参数解析，方便 CLI 的 `tray` 子命令和
// GUI 子系统的 `mytgfs-tray.exe` 共用同一套逻辑。

import (
	"context"

	"mytgfs/internal/config"
	"mytgfs/internal/index"
	"mytgfs/internal/logging"
	"mytgfs/internal/push"
	tgsync "mytgfs/internal/sync"
	"sync"
	"time"
)

func SyncInterval(seconds int) time.Duration {
	switch {
	case seconds < 0:
		return 0
	case seconds == 0:
		return config.DefaultSyncSeconds * time.Second
	default:
		return time.Duration(seconds) * time.Second
	}
}

// StatusNotifier 把自动同步的变更转成 status 事件，面板据此刷新。
//
// 输入：events 推送通道；nil 时返回 nil
// 输出：AutoSyncWhile 的 notify 回调
// 预期：调用方只在真有变化时回调；回调到达就推送，包括只有封面变化（空列表）

func StatusNotifier(events push.Events) func(changed, removed []string) {
	if events == nil {
		return nil
	}
	return func(changed, removed []string) {
		events.Publish(push.TopicStatus)
	}
}

// SyncEnabled 返回一个读取配置文件的自动同步判断。
//
// 输入：cfgPath 配置文件；fallback 读失败时沿用的启动状态
// 输出：每次调用都重新读取 mount.auto_sync / mount.sync_seconds
// 预期：面板改设置后下一轮同步即生效，不需要重启桌面壳

func SyncEnabled(cfgPath string, fallback bool) func() bool {
	return func() bool {
		cur, err := config.Load(cfgPath)
		if err != nil {
			return fallback
		}
		return cur.Mount.SyncOn()
	}
}

// coverInvalidator 是可失效头像元数据缓存的节点源（*telegramstore.Store 实现）。
type coverInvalidator interface {
	InvalidateCover()
}

// AutoSyncWhile 周期性把频道差异应用进索引并通知挂载点刷新。
// 每轮先问 enabled：返回 false 时跳过本轮（只等下一个 tick）。
// onPtsMoved 在本轮 pts 有推进时调用（含头像/权限等非文件更新），
// 装配根用它失效头像缓存——事件驱动，不额外轮询 Telegram。

func AutoSyncWhile(ctx context.Context, notify func(changed, removed []string), idx *index.Index, d tgsync.ChannelDiffer, st tgsync.NodeSource, interval time.Duration, mu *sync.Mutex, enabled func() bool, onPtsMoved func()) {
	tick := time.Tick(interval)
	syncOnce := func() (tgsync.ChannelSyncResult, error) {
		if mu != nil {
			mu.Lock()
			defer mu.Unlock()
		}
		return tgsync.SyncChannel(ctx, idx, d, st, 100)
	}
	for {
		if enabled == nil || enabled() {
			res, err := syncOnce()
			if ctx.Err() != nil {
				return
			}
			switch {
			case err != nil:
				logging.Warn("log.autosync_failed", err)
			default:
				if (res.Pulled > 0 || res.Deleted > 0) && notify != nil {
					notify(res.Changed, res.Removed)
				}
				if res.PtsMoved {
					// 频道有任何更新（含换头像）：头像缓存失效重建，
					// 与第三方客户端一致靠更新流失效，不轮询
					if cw, ok := st.(coverInvalidator); ok {
						cw.InvalidateCover()
					}
					if onPtsMoved != nil {
						onPtsMoved()
					}
					if notify != nil {
						notify(nil, nil)
					}
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-tick:
		}
	}
}

// Run 运行托盘主管进程，阻塞到「退出」。
//
// 输入：opts 启动参数
// 输出：只有启动阶段失败才返回 error；正常退出返回 nil
// 预期：网络不可用时托盘与挂载保持存在，客户端自动重连；
//
//	Web 面板只在菜单「打开面板」时启动，「关闭面板」立即停止
