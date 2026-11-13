package main

// `mytgfs config` 子命令：与面板的配置卡片等价。
//
// 用法：
//   mytgfs config show            打印 config.toml 原文（面板里看到的就是这份）
//   mytgfs config get <section.key>
//   mytgfs config set <section.key> <value>
//   mytgfs config keys            列出可改的键，供脚本/AI 发现
//
// 输入：见上；--json 输出结构化结果
// 输出：成功退出 0；未知键或非法取值退出 1；参数缺失退出 2
// 预期：set 只改一个键，注释与其它段落保持原样

import (
	"flag"
	"fmt"
	"os"

	"mytgfs/internal/config"
	"mytgfs/internal/i18n"
	"mytgfs/internal/ui"
)

// configPath 是命令行默认配置文件。
const configPath = "config.toml"

// runConfig 处理 config 的子命令。
func runConfig(args []string) {
	if len(args) == 0 {
		usage()
		os.Exit(2)
	}
	switch args[0] {
	case "show":
		runConfigShow(args[1:])
	case "get":
		runConfigGet(args[1:])
	case "set":
		runConfigSet(args[1:])
	case "keys":
		runConfigKeys(args[1:])
	default:
		usage()
		os.Exit(2)
	}
}

// runConfigShow 输出配置文件原文。
//
// 输入：无位置参数
// 输出：文件内容；--json 为 {path, content}
// 预期：文件不存在时报错退出 1
func runConfigShow(args []string) {
	fs := flag.NewFlagSet("config show", flag.ExitOnError)
	_ = fs.Parse(args)
	data, err := os.ReadFile(configPath)
	if err != nil {
		fatal(fmt.Sprintf("%s: %v", i18n.T("err.config_load"), err))
	}
	ui.Print(map[string]any{"path": configPath, "content": string(data)})
	ui.Printf("%s", string(data))
}

// runConfigGet 读取单个配置项。
//
// 输入：位置参数 section.key
// 输出：值；键不存在时退出 1
// 预期：--json 输出 {key, value, found}
func runConfigGet(args []string) {
	fs := flag.NewFlagSet("config get", flag.ExitOnError)
	_ = fs.Parse(args)
	if fs.NArg() < 1 {
		usage()
		os.Exit(2)
	}
	key := fs.Arg(0)
	value, found, err := config.GetKey(configPath, key)
	if err != nil {
		fatal(err.Error())
	}
	ui.Print(map[string]any{"key": key, "value": value, "found": found})
	if !found {
		fatal(i18n.T("err.config_key_missing", key))
	}
	ui.Printf("%s = %s\n", key, value)
}

// runConfigSet 写入单个配置项。
//
// 输入：位置参数 section.key、value
// 输出：SET 行
// 预期：只改这一个键；未知键或非法取值退出 1
func runConfigSet(args []string) {
	fs := flag.NewFlagSet("config set", flag.ExitOnError)
	_ = fs.Parse(args)
	if fs.NArg() < 2 {
		usage()
		os.Exit(2)
	}
	key, value := fs.Arg(0), fs.Arg(1)
	if err := config.SetKey(configPath, key, value); err != nil {
		fatal(err.Error())
	}
	ui.Print(map[string]any{"key": key, "value": value})
	ui.Printf("SET %s = %s\n", key, value)
}

// runConfigKeys 列出可读写的键。
//
// 输入：无位置参数
// 输出：每行一个键；--json 为 keys 数组
// 预期：供脚本与 AI 枚举，不需要读源码
func runConfigKeys(args []string) {
	fs := flag.NewFlagSet("config keys", flag.ExitOnError)
	_ = fs.Parse(args)
	keys := config.Keys()
	ui.Print(map[string]any{"keys": keys})
	for _, k := range keys {
		ui.Printf("%s\n", k)
	}
}
