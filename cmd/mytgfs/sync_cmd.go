package main

// mytgfs CLI 入口。
//
// 用法：
//   mytgfs auth bot                        bot 登录
//   mytgfs auth user                       手机号登录
//   mytgfs channel init --bot <username>   建私有频道并给 bot 提权
//   mytgfs sync [--limit N]                拉频道历史重建本地索引

import (
	"context"
	"flag"
	"fmt"
	"mytgfs/internal/app"
	"mytgfs/internal/i18n"
	tgsync "mytgfs/internal/sync"
	"mytgfs/internal/ui"
	"os"
	"os/signal"
)

func runSync(args []string) {
	fs := flag.NewFlagSet("sync", flag.ExitOnError)
	limit := fs.Int("limit", 100, i18n.T("cli.flag.limit_sync"))
	name := fs.String("name", "", i18n.T("cli.flag.volume"))
	rebuild := fs.Bool("rebuild", false, i18n.T("cli.flag.rebuild"))
	_ = fs.Parse(args)

	a, err := app.Load(app.Options{})
	if err != nil {
		fatal(fmt.Sprintf("%s: %v", i18n.T("err.config_read"), err))
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	entries := 0
	stats := map[string]any{}
	err = a.Run(ctx, *name, func(ctx context.Context, s *app.Session) error {
		if *rebuild {
			nodes, rerr := a.AllNodes(ctx, s.Volume.ChatID)
			if rerr != nil {
				return rerr
			}
			n, rerr := tgsync.Rebuild(ctx, s.Index, nodes)
			if rerr != nil {
				return rerr
			}
			stats["mode"] = "rebuild"
			stats["nodes"] = n
			ui.Out(i18n.T("cli.rebuild_stats", n))
		} else {
			d := &tgsync.TelegramChannelDiffer{API: s.Client.API(), ChannelID: s.Volume.ChatID}
			res, serr := tgsync.SyncChannel(ctx, s.Index, d, s.Store(), *limit)
			if serr != nil {
				return serr
			}
			if res.Truncated {
				ui.Out(i18n.T("warn.diff_too_long"))
			}
			stats["mode"] = "sync"
			stats["pulled"] = res.Pulled
			stats["deleted"] = res.Deleted
			stats["pts"] = res.Pts
			stats["rounds"] = res.Rounds
			stats["truncated"] = res.Truncated
			ui.Out(i18n.T("cli.sync_stats", res.Pulled, res.Deleted, res.Pts, res.Rounds))
		}
		children, cerr := s.Index.Children(ctx, 0)
		if cerr != nil {
			return cerr
		}
		entries = len(children)
		stats["volume"] = s.Volume.Name
		stats["entries"] = entries
		return nil
	})
	if err != nil {
		failErr(i18n.T("err.sync"), err)
	}
	ui.Print(stats)
	ui.Out(i18n.T("cli.sync_ok", entries))
}
