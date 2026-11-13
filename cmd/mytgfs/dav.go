package main

// `mytgfs webdav` 子命令：把频道内容以 WebDAV 暴露给系统客户端。
//
// 用法：mytgfs webdav [--addr 127.0.0.1:8080] [--name mytgfs]
// 输入：--addr 监听地址；--name 卷名
// 输出：启动信息；失败退出 1
// 预期：只监听本机回环地址；资源管理器可用“映射网络驱动器”连上来

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"time"

	"mytgfs/internal/app"
	"mytgfs/internal/datadir"
	"mytgfs/internal/dav"
	"mytgfs/internal/i18n"
	"mytgfs/internal/netmap"
	"mytgfs/internal/queue"
)

// runWebDAV 启动 WebDAV 服务。
func runWebDAV(args []string) {
	fs := flag.NewFlagSet("webdav", flag.ExitOnError)
	addr := fs.String("addr", "127.0.0.1:8080", i18n.T("cli.flag.addr_webdav"))
	name := fs.String("name", "", i18n.T("cli.flag.volume"))
	_ = fs.Parse(args)

	a, err := app.Load(app.Options{})
	if err != nil {
		fatal(fmt.Sprintf("%s: %v", i18n.T("err.config_load"), err))
	}
	_, volName, ok := a.Volume(*name)
	if !ok {
		fatal(i18n.T("err.volume_not_found", volName))
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	// 退出时删掉指向本服务的网络映射，避免留下红叉盘符
	defer netmap.Cleanup(*addr)

	err = a.Run(ctx, volName, func(ctx context.Context, s *app.Session) error {
		fsys := dav.New(ctx, s.Index, s.Store(), a.Config().Mount.ReadChunk())
		// 写入走串行队列：批量拷贝时不会互相抢 Telegram 的发消息配额
		fsys.SetQueue(queue.New(1, 3))
		// 上传临时文件放数据目录，异常中断的残留由 dav 按时间清扫
		fsys.SetTempDir(datadir.Path("tmp"))
		// 重启后按账本补传上次未提交的文件，只处理本程序记下的记录
		if rerr := fsys.ResumePending(); rerr != nil {
			fmt.Fprintf(os.Stderr, "pending resume: %v\n", rerr)
		}
		handler := dav.NewHandler(fsys, nil)
		srv := &http.Server{Addr: *addr, Handler: handler, ReadHeaderTimeout: 10 * time.Second}

		// AfterFunc 在 ctx 取消时起协程收尾，不再常驻一个等待 goroutine
		context.AfterFunc(ctx, func() {
			shutdown, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancelShutdown()
			_ = srv.Shutdown(shutdown)
		})

		fmt.Printf("webdav listening on http://%s/ (volume %s)\n", *addr, volName)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			return err
		}
		return nil
	})
	if err != nil {
		failErr(i18n.T("err.webdav_prefix"), err)
	}
}

// runUnmap 删除指向指定地址的网络映射。
//
// 用法：mytgfs unmap [--addr 127.0.0.1:8080]
// 输入：--addr 服务地址
// 输出：删除结果
// 预期：只删与本服务地址匹配的映射，其它盘符不动
func runUnmap(args []string) {
	fs := flag.NewFlagSet("unmap", flag.ExitOnError)
	addr := fs.String("addr", "127.0.0.1:8080", i18n.T("cli.flag.addr"))
	_ = fs.Parse(args)
	netmap.Cleanup(*addr)
	fmt.Println(i18n.T("dav.cleanup_done", *addr))
}
