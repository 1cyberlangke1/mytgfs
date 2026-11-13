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

// Print 在 JSON 模式下输出一条成功结果。
//
// 输入：data 可序列化的结果
// 输出：一行 JSON（默认模式下什么都不做）
// 预期：序列化失败时退回错误输出，不 panic
func Print(data any) {
	if !JSONMode() {
		return
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(map[string]any{"ok": true, "data": data}); err != nil {
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
