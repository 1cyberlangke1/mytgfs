package vfs

import (
	"cmp"
	"slices"
)

// 下载进度跟踪：按卷内路径记录已读字节。
//
// 输入：读取过程中的已读偏移与总大小
// 输出：Transfers() 返回按路径排序的快照
// 预期：小于 1MB 的文件不登记，读完即移除

type Transfer struct {
	Path  string `json:"path"`
	Done  int64  `json:"done"`
	Total int64  `json:"total"`
	// Kind 标明这条进度来自真正的下载任务还是媒体播放读取。
	// 播放读取不属于操作队列，前端据此过滤，不再显示成「下载」。
	Kind string `json:"kind,omitempty"`
}

// 传输类型：下载任务与播放读取。
const (
	TransferDownload = "download"
	TransferStream   = "stream"
)

// transferMinSize 是进入进度列表的最小文件体积。
//
// 小于这个值的文件通常一个分块就读完，显示进度只会让面板闪一下。
const transferMinSize = 1 << 20

// UploadProgressFunc 是上传字节进度回调。
//
// 输入：path 卷内目标路径；done 已上传字节；total 文件总大小
// 输出：无
// 预期：由上传工作协程调用，实现必须快速返回，不能阻塞上传
type UploadProgressFunc func(path string, done, total int64)

// SetUploadProgress 设置上传进度回调。
//
// 输入：fn 回调；nil 表示关闭
// 输出：无
// 预期：命令行用它显示进度；面板不设置，继续走队列推送
func (d *FileSystem) SetUploadProgress(fn UploadProgressFunc) {
	if fn == nil {
		d.upProg.Store(nil)
		return
	}
	d.upProg.Store(&fn)
}

// reportUpload 把一次上传进度转发给调用方。
//
// 输入：path 目标路径；done 已传字节；total 总大小
// 输出：无
// 预期：没有设置回调时什么都不做
func (d *FileSystem) reportUpload(path string, done, total int64) {
	if p := d.upProg.Load(); p != nil {
		(*p)(path, done, total)
	}
}

// track 记录一次传输进度。
//
// 输入：name 卷内路径；done 已取回字节；total 文件总大小；kind 来源类型
// 输出：无
// 预期：小于 transferMinSize 或已取完时移除记录；done 只增不减
func (d *FileSystem) track(name string, done, total int64, kind string) {
	if total < transferMinSize {
		return
	}
	if kind == "" {
		kind = TransferDownload
	}
	if done > total {
		done = total
	}
	d.trMu.Lock()
	if done >= total {
		delete(d.tr, name)
		d.trMu.Unlock()
		d.notifyProgress()
		return
	}
	if d.tr == nil {
		d.tr = make(map[string]*Transfer)
	}
	t := d.tr[name]
	if t == nil {
		d.tr[name] = &Transfer{Path: name, Done: done, Total: total, Kind: kind}
	} else {
		t.Kind = kind
		if done > t.Done {
			t.Done = done
		}
	}
	d.trMu.Unlock()
	d.notifyProgress()
}

// untrack 移除一条下载进度。
func (d *FileSystem) untrack(name string) {
	d.trMu.Lock()
	delete(d.tr, name)
	d.trMu.Unlock()
	d.notifyProgress()
}

// Transfers 返回当前下载进度快照，按路径排序。
func (d *FileSystem) Transfers() []Transfer {
	d.trMu.Lock()
	defer d.trMu.Unlock()
	out := make([]Transfer, 0, len(d.tr))
	for _, t := range d.tr {
		out = append(out, *t)
	}
	slices.SortFunc(out, func(a, b Transfer) int { return cmp.Compare(a.Path, b.Path) })
	return out
}

// 上传临时文件的命名与保留时长。
//
// 异常中断（关标签页、拔网线、杀进程）时 Close 不会被调用，
