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

	"mytgfs/internal/app"
	"mytgfs/internal/fsck"
	"mytgfs/internal/i18n"
	"mytgfs/internal/telegramstore"
	"mytgfs/internal/ui"
)

// checkOutput 是 --json 模式下的对账结果。
type checkOutput struct {
	Volume   string         `json:"volume"`
	Checked  int            `json:"checked"`
	Missing  int            `json:"missing"`
	Moved    int            `json:"moved"`
	Added    int            `json:"added"`
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
	name := fs.String("name", "", i18n.T("cli.flag.volume"))
	fix := fs.Bool("fix", false, i18n.T("cli.flag.fix"))
	_ = fs.Parse(args)

	a, err := app.Load(app.Options{})
	if err != nil {
		fatal(fmt.Sprintf("%s: %v", i18n.T("err.config_load"), err))
	}
	volume, volName, ok := a.Volume(*name)
	if !ok {
		fatal(i18n.T("err.volume_not_found", volName))
	}
	if volume.ChatID == 0 {
		fatal(i18n.T("err.volume_no_channel", volName))
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	var rep fsck.Report
	nodes, err := a.AllNodes(ctx, volume.ChatID)
	if err != nil {
		failErr(i18n.T("err.check_prefix"), err)
	}
	err = a.Run(ctx, volName, func(ctx context.Context, s *app.Session) error {
		var xerr error
		rep, xerr = fsck.Run(ctx, s.Index, nodeSource(nodes), *fix)
		return xerr
	})
	if err != nil {
		failErr(i18n.T("err.check_prefix"), err)
	}

	if ui.JSONMode() {
		ui.Print(checkOutput{
			Volume: volName, Checked: rep.Checked, Missing: rep.Missing,
			Moved: rep.Moved, Added: rep.Added, Fixed: rep.Fixed, Problems: rep.Problems,
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
		case fsck.KindAdded:
			fmt.Println(i18n.T("check_problem_added", p.Path, p.MsgID))
		}
	}
	if len(rep.Problems) == 0 {
		fmt.Println(i18n.T("check_clean", rep.Checked))
		return
	}
	fmt.Println(i18n.T("check_summary", rep.Checked, rep.Missing, rep.Moved, rep.Added, rep.Fixed))
	if !*fix {
		fmt.Println(i18n.T("check_fix_hint"))
	}
}

// nodeSource 把一次拉好的全量节点包成 fsck.Source。
type nodeSource []telegramstore.NodeInfo

// AllNodes 返回内存快照。
func (n nodeSource) AllNodes(context.Context) ([]telegramstore.NodeInfo, error) {
	return n, nil
}
