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
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"time"

	"golang.org/x/net/webdav"

	"mytgfs/internal/config"
	"mytgfs/internal/crypto"
	"mytgfs/internal/datadir"
	"mytgfs/internal/dav"
	"mytgfs/internal/i18n"
	"mytgfs/internal/index"
	"mytgfs/internal/sessionstore"
	"mytgfs/internal/telegramstore"
	"mytgfs/internal/tgclient"
)

// runWebDAV 启动 WebDAV 服务。
func runWebDAV(args []string) {
	fs := flag.NewFlagSet("webdav", flag.ExitOnError)
	addr := fs.String("addr", "127.0.0.1:8080", "监听地址")
	name := fs.String("name", "mytgfs", "卷名")
	_ = fs.Parse(args)

	cfg, err := config.Load("config.toml")
	if err != nil {
		fatal(fmt.Sprintf("读取配置: %v", err))
	}
	volume, ok := volumeByName(cfg, *name)
	if !ok {
		fatal("卷不存在: " + *name)
	}
	apiHash := cfg.APIHash()
	if apiHash == "" {
		fatal(i18n.T("err.missing_api_hash"))
	}
	key, err := crypto.LoadOrCreateKey("secrets/session.key")
	if err != nil {
		fatal(fmt.Sprintf("%s: %v", i18n.T("err.session_key_prefix"), err))
	}
	idx, err := index.Open(datadir.Path("index-" + *name + ".db"))
	if err != nil {
		fatal(fmt.Sprintf("打开索引: %v", err))
	}
	defer idx.Close()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	// 退出时删掉指向本服务的网络映射，避免留下红叉盘符
	defer cleanupMappings(*addr)

	client := tgclient.New(cfg.Telegram.APIID, apiHash,
		&sessionstore.Encrypted{Path: "secrets/bot.session", Key: key},
		tgclient.Options{})

	err = client.Run(ctx, func(ctx context.Context) error {
		store := telegramstore.New(client.API(), volume.ChatID)
		store.SetParallel(cfg.Mount.DownloadConnections())
		handler := &webdav.Handler{
			Prefix:     "/",
			FileSystem: dav.New(ctx, idx, store),
			LockSystem: webdav.NewMemLS(),
		}
		srv := &http.Server{Addr: *addr, Handler: handler, ReadHeaderTimeout: 10 * time.Second}

		go func() {
			<-ctx.Done()
			shutdown, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancelShutdown()
			_ = srv.Shutdown(shutdown)
		}()

		fmt.Printf("webdav listening on http://%s/ (volume %s)\n", *addr, *name)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			return err
		}
		return nil
	})
	if err != nil {
		failErr("webdav 失败", err)
	}
}

// cleanupMappings 删除指向本服务地址的网络映射。
//
// 输入：addr 监听地址（如 127.0.0.1:8080）
// 输出：无
// 预期：只处理命令行里包含该地址的映射；解析失败时不动任何盘符
func cleanupMappings(addr string) {
	if runtime.GOOS != "windows" {
		return
	}
	out, err := exec.Command("net", "use").Output()
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.Contains(line, addr) {
			continue
		}
		for _, f := range strings.Fields(line) {
			if len(f) == 2 && strings.HasSuffix(f, ":") {
				_ = exec.Command("net", "use", f, "/delete", "/y").Run()
			}
		}
	}
}
