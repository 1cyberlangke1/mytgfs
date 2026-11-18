package ui

// 面向脚本与 AI 的结构化输出。
//
// 约定：
//   - 只有显式 `--json` 才输出 JSON；默认仍是人读文本
//   - 成功：{"ok":true,"data":...}
//   - 失败：{"ok":false,"error":{"code":"...","message":"..."}}
//   - 退出码稳定：0 成功、1 运行失败、2 用法错误

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
)

var (
	mu       sync.RWMutex
	jsonMode bool
	// outputMu/outputErr 记录「结果没能写出去」：编码失败时 main 要返回非零退出码，
	// 否则脚本会把写失败当成成功
	outputMu  sync.Mutex
	outputErr error
)

// EnableJSON 打开 JSON 输出。
func EnableJSON() {
	mu.Lock()
	jsonMode = true
	mu.Unlock()
}

// JSONMode 返回是否已打开 JSON 输出。
func JSONMode() bool {
	mu.RLock()
	defer mu.RUnlock()
	return jsonMode
}

// DisableJSON 关闭 JSON 输出，恢复默认的人读文本。
//
// 输入：无
// 输出：无
// 预期：与 EnableJSON 对称；命令行只在启动时按 --json 选择一次，
// 测试需要确定输出形态时用它复位全局状态
func DisableJSON() {
	mu.Lock()
	jsonMode = false
	mu.Unlock()
}

// OutputFailed 报告最近一次 JSON 输出是否编码失败。
//
// 输入：无
// 输出：true 表示有结果没能写出去
// 预期：main 据此返回非零退出码，脚本不会把「写失败」当成成功
func OutputFailed() bool {
	outputMu.Lock()
	defer outputMu.Unlock()
	return outputErr != nil
}

// Print 在 JSON 模式下输出一条成功结果。
//
// 输入：data 可序列化的结果
// 输出：一行 JSON（默认模式下什么都不做）
// 预期：序列化失败时退回错误输出并记下失败状态，不 panic
func Print(data any) {
	if !JSONMode() {
		return
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(map[string]any{"ok": true, "data": data}); err != nil {
		outputMu.Lock()
		outputErr = err
		outputMu.Unlock()
		Error("encode", err)
	}
}

// Error 在 JSON 模式下输出结构化错误到 stderr。
func Error(code string, err error) {
	if !JSONMode() {
		return
	}
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	enc := json.NewEncoder(os.Stderr)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(map[string]any{
		"ok":    false,
		"error": map[string]string{"code": code, "message": msg},
	})
}

// Printf 在非 JSON 模式下打印人读文本；JSON 模式下不下发冗余文案。
func Printf(format string, args ...any) {
	if JSONMode() {
		return
	}
	fmt.Printf(format, args...)
}

// Out 在非 JSON 模式下把一行人读结果写到 stdout；JSON 模式下不下发冗余文案。
func Out(args ...any) {
	if JSONMode() {
		return
	}
	fmt.Println(args...)
}

// Outf 在非 JSON 模式下把格式化结果写到 stdout；JSON 模式下不下发冗余文案。
func Outf(format string, args ...any) {
	if JSONMode() {
		return
	}
	fmt.Printf(format, args...)
}

// Errf 在非 JSON 模式下把一行人读错误写到 stderr；JSON 模式由 Error 负责。
func Errf(format string, args ...any) {
	if JSONMode() {
		return
	}
	fmt.Fprintf(os.Stderr, format, args...)
}
