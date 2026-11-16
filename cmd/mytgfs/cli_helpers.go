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
	"errors"
	"fmt"
	"mytgfs/internal/i18n"
	"mytgfs/internal/tgclient"
	"mytgfs/internal/ui"
	"os"
	"strings"
)

func usage() {
	if ui.JSONMode() {
		ui.Error("usage", fmt.Errorf("%s", i18n.T("cli.usage_short")))
		os.Exit(2)
	}
	ui.Errf("%s", i18n.T("cli.usage_head"))
	if !cliOnlyBuild {
		ui.Errf("%s", i18n.T("cli.usage_ui"))
	}
	ui.Errf("%s\n", i18n.T("cli.usage_tail"))
}

func readTrim(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func readLine() string {
	var s string
	fmt.Fscanln(os.Stdin, &s)
	return strings.TrimSpace(s)
}

// stripJSONFlag 取出全局 --json 开关并返回其余参数。
//
// 输入：os.Args[1:]
// 输出：去掉所有 --json 的参数
// 预期：开关位置无关；出现在参数值里不会被误删（只匹配独立的 --json）

func stripJSONFlag(args []string) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		if a == "--json" {
			ui.EnableJSON()
			continue
		}
		out = append(out, a)
	}
	return out
}

func fatal(msg string) {
	fatalCode("fatal", msg)
}

// fatalCode 以指定错误码退出。
//
// 输入：code 机器可读错误码；msg 人读信息
// 输出：无（进程退出 1）；JSON 模式下发结构化错误到 stderr
// 预期：人读模式与 fatal 完全一致，只有 JSON 的错误码不同
func fatalCode(code, msg string) {
	if ui.JSONMode() {
		ui.Error(code, fmt.Errorf("%s", msg))
		os.Exit(1)
	}
	ui.Errf("FAIL: %s\n", msg)
	os.Exit(1)
}

// failErr 打印错误；授权失效时补一句重新登录提示。
//
// 输入：prefix 错误前缀；err 原始错误
// 输出：无（进程退出 1）
// 预期：会话失效不可自愈，提示重新 auth，避免用户反复重试同一命令

func failErr(prefix string, err error) {
	code := "error"
	switch {
	case tgclient.IsAuthLost(err):
		code = "auth_lost"
	case errors.Is(err, os.ErrNotExist):
		code = "not_found"
	case errors.Is(err, context.Canceled):
		code = "canceled"
	}
	if ui.JSONMode() {
		ui.Error(code, fmt.Errorf("%s: %w", prefix, err))
		os.Exit(1)
	}
	if code == "auth_lost" {
		fatal(fmt.Sprintf("%s: %v\n%s", prefix, err, i18n.T("err.auth_lost_hint")))
	}
	fatal(fmt.Sprintf("%s: %v", prefix, err))
}
