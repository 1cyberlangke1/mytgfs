package main

// 挂载进程控制：状态文件 + 安全卸载命令。
//
// 输入：data/mount.json（挂载进程写）、mytgfs umount --point Y:
// 输出：卸载成功提示
// 预期：IPC 通知挂载进程解除 WinFsp 挂载并退出，盘符随之消失，和拔 U 盘一致

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"mytgfs/internal/atomicfile"
	"mytgfs/internal/datadir"
	"mytgfs/internal/ipc"
)

// mountState 是挂载进程的控制信息。
type mountState struct {
	Addr   string   `json:"addr"`
	Token  string   `json:"token"`
	Points []string `json:"points"`
}

// mountStatePath 返回挂载状态文件路径。
func mountStatePath() string { return datadir.Path("mount.json") }

// newToken 生成 16 字节随机 token。
func newToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// writeMountState 原子写挂载状态文件。
func writeMountState(s mountState) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.WriteFile(mountStatePath(), b, 0o600)
}

// removeMountState 删除挂载状态文件。
func removeMountState() { _ = os.Remove(mountStatePath()) }

// umountPointIfRunning 请求卸载指定挂载点。
//
// 输入：point 挂载点，例如 "Y:"
// 输出：error；没有挂载进程时返回 nil（视为已完成）
// 预期：发出 IPC 后最多等 5 秒让进程清理；超时不视为失败，
// 因为同一进程可能还挂着别的卷
func umountPointIfRunning(point string) error {
	b, err := os.ReadFile(mountStatePath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var st mountState
	if err := json.Unmarshal(b, &st); err != nil {
		return err
	}
	if _, err := ipc.Call(context.Background(), st.Addr, st.Token, "umount", map[string]string{"point": point}); err != nil {
		// 连接不上说明挂载进程已经死了，留下的是崩溃残留的状态文件；
		// 清掉它并视为已卸载，否则每次删卷都会报一次假失败
		removeMountState()
		return nil
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(mountStatePath()); os.IsNotExist(err) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil
}

// runUmount 安全移除挂载点。
//
// 输入：--point 挂载点（省略表示全部）
// 输出：成功提示
// 预期：找不到状态文件或连接失败时明确报错，不静默成功
func runUmount(args []string) {
	fs := flag.NewFlagSet("umount", flag.ExitOnError)
	point := fs.String("point", "", "挂载点，例如 Y:；省略表示全部")
	_ = fs.Parse(args)

	b, err := os.ReadFile(mountStatePath())
	if err != nil {
		fatal("没有正在运行的挂载（找不到 data/mount.json）")
	}
	var st mountState
	if err := json.Unmarshal(b, &st); err != nil {
		fatal(fmt.Sprintf("读取挂载状态: %v", err))
	}

	data := map[string]string{}
	if *point != "" {
		data["point"] = *point
	}
	if _, err := ipc.Call(context.Background(), st.Addr, st.Token, "umount", data); err != nil {
		fatal(fmt.Sprintf("卸载失败: %v", err))
	}

	// 等进程真正退出，状态文件删除即视为完成
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(mountStatePath()); os.IsNotExist(err) {
			fmt.Println("OK unmounted")
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	fmt.Println("OK 已请求卸载")
}
