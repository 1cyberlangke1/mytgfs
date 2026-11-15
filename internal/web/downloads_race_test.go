package web

// 下载任务的竞态回归：取消后残留协程不能把内容改名成成品，
// 也不能让已取消的任务被 Resume 拉起来。

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"mytgfs/internal/fm"
	"mytgfs/internal/vfs"
)

// gatedFile 第一次 Read 先等测试放行，用来把「取消」卡在复制途中。
type gatedFile struct {
	*os.File
	started chan struct{}
	gate    chan struct{}
	once    *sync.Once
}

func (f *gatedFile) Read(p []byte) (int, error) {
	f.once.Do(func() {
		close(f.started)
		<-f.gate
	})
	return f.File.Read(p)
}

func (f *gatedFile) Write([]byte) (int, error) { return 0, fs.ErrInvalid }
func (f *gatedFile) Readdir(int) ([]fs.FileInfo, error) {
	return nil, fs.ErrInvalid
}

// newGatedStore 建一个读取会卡住的后台下载环境。
//
// 输入：t 测试；size 源文件大小
// 输出：下载管理器、下载目录、started 首次读取信号、gate 放行通道
// 预期：Start 后协程停在第一次 Read，取消/继续都由测试决定时点
func newGatedStore(t *testing.T, size int) (*downloadStore, string, chan struct{}, chan struct{}) {
	t.Helper()
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "big.bin"), bytes.Repeat([]byte("x"), size), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	started := make(chan struct{})
	gate := make(chan struct{})
	once := new(sync.Once)
	dl := newDownloadStore(func() string { return dir }, func(string) (fm.Files, bool) {
		return fmFunc(func(_ context.Context, p string) (vfs.File, error) {
			return &gatedFile{File: mustOpen(src, p), started: started, gate: gate, once: once}, nil
		}), true
	}, nil)
	return dl, dir, started, gate
}

// waitDirEmpty 等目录彻底清空（临时文件也算）。
func waitDirEmpty(t *testing.T, dir string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) == 0 {
			return
		}
		if time.Now().After(deadline) {
			names := make([]string, 0, len(entries))
			for _, e := range entries {
				names = append(names, e.Name())
			}
			t.Fatalf("取消后目录应清空，实际 %v", names)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestCancelDuringRunLeavesNoFile 复制途中取消：不落成品，也不留临时文件。
func TestCancelDuringRunLeavesNoFile(t *testing.T) {
	dl, dir, started, gate := newGatedStore(t, 1<<20)

	task, err := dl.Start("demo", "/big.bin")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	<-started
	if err := dl.Cancel(task.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	close(gate) // 放行残留协程：它读完后不允许改名成成品

	waitDirEmpty(t, dir)
	if got := dl.Tasks(); len(got) != 0 {
		t.Fatalf("任务应已移除: %+v", got)
	}
	// 取消后 Resume 也不能把任务拉起来
	if err := dl.Resume(task.ID); !errors.Is(err, errDownloadTask) {
		t.Fatalf("Resume err = %v，期望 errDownloadTask", err)
	}
}

// TestPausedTaskDoesNotFinishAfterCancel 取消后即便读到了 EOF 也只清理，不落盘。
func TestPausedTaskDoesNotFinishAfterCancel(t *testing.T) {
	dl, dir, started, gate := newGatedStore(t, 4<<10)

	task, err := dl.Start("demo", "/big.bin")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	<-started
	// 先暂停再取消：两条路径都不能让协程在取消后继续落成品
	if err := dl.Pause(task.ID); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	if err := dl.Cancel(task.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	close(gate)

	waitDirEmpty(t, dir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), ".") {
			t.Fatalf("不该出现成品文件: %s", e.Name())
		}
	}
}
