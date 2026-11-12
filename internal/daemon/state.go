package daemon

// daemon 状态文件 daemon.json。

import (
	"bytes"
	"encoding/json"
	"os"

	"github.com/natefinch/atomic"
	"mytgfs/internal/datadir"
)

// StatePath 返回 daemon 状态文件路径。
//
// 输入：无
// 输出：data/daemon.json
// 预期：运行时文件集中放在 data 目录
func StatePath() string { return datadir.Path("daemon.json") }

// State 是 daemon 运行状态。
type State struct {
	Addr  string `json:"addr"`
	Token string `json:"token"`
	PID   int    `json:"pid"`
}

// ReadState 读取状态文件。
func ReadState() (State, error) {
	var s State
	b, err := os.ReadFile(StatePath())
	if err != nil {
		return s, err
	}
	err = json.Unmarshal(b, &s)
	return s, err
}

// writeState 原子写状态文件。
func writeState(s State) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return atomic.WriteFile(StatePath(), bytes.NewReader(b))
}

// removeState 删除状态文件。
func removeState() {
	_ = os.Remove(StatePath())
}
