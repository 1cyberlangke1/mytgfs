package main

// check 子命令：把本地索引与频道里的消息逐条对账，可选就地修复。
//
// 对账只读取自己索引里记过的消息号，不会遍历或删除频道里别的内容。

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"

	"mytgfs/internal/channel"
	"mytgfs/internal/config"
	"mytgfs/internal/crypto"
	"mytgfs/internal/datadir"
	"mytgfs/internal/fsck"
	"mytgfs/internal/i18n"
	"mytgfs/internal/index"
	"mytgfs/internal/sessionstore"
	"mytgfs/internal/tgclient"
	"mytgfs/internal/ui"
)

// checkOutput 是 --json 模式下的对账结果。
type checkOutput struct {
	Volume   string         `json:"volume"`
	Checked  int            `json:"checked"`
	Missing  int            `json:"missing"`
	Moved    int            `json:"moved"`
	Fixed    int            `json:"fixed"`
	Problems []fsck.Problem `json:"problems"`
}

// runCheck 处理索引对账。
//
// 输入：命令行参数（--name 卷名、--fix 修复）
// 输出：无（打印问题与统计；失败退出）
// 预期：--fix 删除缺失行、按 caption 修正路径；不加时只报告
func runCheck(args []string) {
	fs := flag.NewFlagSet("check", flag.ExitOnError)
	name := fs.String("name", "", "卷名（空则用第一个卷）")
	fix := fs.Bool("fix", false, "按核对结果修复索引")
	_ = fs.Parse(args)

	cfg, err := config.Load("config.toml")
	if err != nil {
		fatal(fmt.Sprintf("%s: %v", i18n.T("err.config_load"), err))
	}
	volName := resolveVolumeName(cfg, *name)
	volume, ok := volumeByName(cfg, volName)
	if !ok {
		fatal(i18n.T("err.volume_not_found", volName))
	}
	if volume.ChatID == 0 {
		fatal(i18n.T("err.volume_no_channel", volName))
	}
	apiHash := cfg.APIHash()
	if apiHash == "" {
		fatal(i18n.T("err.missing_api_hash"))
	}
	key, err := crypto.LoadOrCreateKey("secrets/session.key")
	if err != nil {
		fatal(fmt.Sprintf("%s: %v", i18n.T("err.session_key_prefix"), err))
	}
	idx, err := index.Open(datadir.Path("index-" + volName + ".db"))
	if err != nil {
		fatal(fmt.Sprintf(i18n.T("err.index_open"), err))
	}
	defer idx.Close()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	var rep fsck.Report
	client := tgclient.New(cfg.Telegram.APIID, apiHash,
		&sessionstore.Encrypted{Path: "secrets/bot.session", Key: key},
		tgclient.Options{})
	err = client.Run(ctx, func(ctx context.Context) error {
		ch, rerr := channel.Resolve(ctx, client.API(), volume.ChatID)
		if rerr != nil {
			return rerr
		}
		var xerr error
		rep, xerr = fsck.Run(ctx, idx, client.API(), ch.ChannelID, ch.AccessHash, *fix)
		return xerr
	})
	if err != nil {
		failErr(i18n.T("err.check_prefix"), err)
	}

	if ui.JSONMode() {
		ui.Print(checkOutput{
			Volume: volName, Checked: rep.Checked, Missing: rep.Missing,
			Moved: rep.Moved, Fixed: rep.Fixed, Problems: rep.Problems,
		})
		return
	}
	fmt.Println(i18n.T("check_title", volName, volume.ChatID))
	for _, p := range rep.Problems {
		switch p.Kind {
		case fsck.KindMissing:
			fmt.Println(i18n.T("check_problem_missing", p.Path, p.MsgID))
		case fsck.KindMoved:
			fmt.Println(i18n.T("check_problem_moved", p.Path, p.Actual, p.MsgID))
		}
	}
	if len(rep.Problems) == 0 {
		fmt.Println(i18n.T("check_clean", rep.Checked))
		return
	}
	fmt.Println(i18n.T("check_summary", rep.Checked, rep.Missing, rep.Moved, rep.Fixed))
	if !*fix {
		fmt.Println(i18n.T("check_fix_hint"))
	}
}
