package main

// 下载进度：每 200ms 向 stderr 打一行覆盖式状态。
//
// 输入：写入的字节流
// 输出：stderr 上的百分比、已传/总量、平均速度；--json 或 "-" 直出时关闭
// 预期：进度只影响人读体验，stdout 内容与退出码不受影响

import (
	"fmt"
	"io"
	"time"
)

// progressInterval 是刷新进度行的最小间隔。
const progressInterval = 200 * time.Millisecond

// progressWriter 统计已写字节并按间隔刷新进度行。
//
// 输入：dst 真实写入目标；errw 进度输出（通常是 stderr）；total 文件总大小；
// resumed 断点续传已存在的字节数
// 输出：Write 原样转发并累计 done
// 预期：done 从 resumed 起算，百分比按 total 计算
type progressWriter struct {
	dst     io.Writer
	errw    io.Writer
	total   int64
	done    int64
	start   time.Time
	last    time.Time
	enabled bool
}

// newProgressWriter 创建进度写入器。
//
// 输入：dst 目标；errw 进度输出；total 总字节；resumed 续传起点；enabled 是否输出
// 输出：*progressWriter
// 预期：done 初值是 resumed，速度从创建时刻开始算
func newProgressWriter(dst, errw io.Writer, total, resumed int64, enabled bool) *progressWriter {
	now := time.Now()
	return &progressWriter{dst: dst, errw: errw, total: total, done: resumed, start: now, last: now, enabled: enabled}
}

// Write 写入数据并择机刷新进度。
//
// 输入：b 数据块
// 输出：写入字节数、错误（与 dst 一致）
// 预期：间隔不足 200ms 不打印；enabled 为假时完全不碰 errw
func (p *progressWriter) Write(b []byte) (int, error) {
	n, err := p.dst.Write(b)
	p.done += int64(n)
	if p.enabled {
		now := time.Now()
		if now.Sub(p.last) >= progressInterval {
			p.last = now
			p.render(false)
		}
	}
	return n, err
}

// render 输出一行进度。
//
// 输入：final 为真时补换行（下载结束）
// 输出：写到 errw；total<=0 或未启用时什么都不做
// 预期：同一行覆盖刷新，最终那行留下换行
func (p *progressWriter) render(final bool) {
	if !p.enabled || p.total <= 0 {
		return
	}
	elapsed := time.Since(p.start).Seconds()
	var rate int64
	if elapsed > 0 {
		rate = int64(float64(p.done) / elapsed)
	}
	if final {
		fmt.Fprintf(p.errw, "\r%s / %s  %s/s\n", humanBytes(p.done), humanBytes(p.total), humanBytes(rate))
		return
	}
	pct := float64(p.done) / float64(p.total) * 100
	if pct > 100 {
		pct = 100
	}
	fmt.Fprintf(p.errw, "\r%5.1f%%  %s / %s  %s/s", pct, humanBytes(p.done), humanBytes(p.total), humanBytes(rate))
}

// uploadReporter 把上传进度打到 stderr。
//
// 输入：目标路径、已传/总字节
// 输出：stderr 上覆盖刷新的一行；结束时补换行
// 预期：同名文件才刷新，切换文件时重置计时
type uploadReporter struct {
	errw  io.Writer
	name  string
	start time.Time
	last  time.Time
	// open 表示 stderr 上还有一行没换行，end 负责收尾
	open bool
}

// newUploadReporter 创建上传进度输出器。
//
// 输入：errw 进度输出（通常是 stderr）
// 输出：*uploadReporter
// 预期：每个文件第一次回调只记录起点，不打印 0%
func newUploadReporter(errw io.Writer) *uploadReporter {
	return &uploadReporter{errw: errw}
}

// report 实现 vfs.UploadProgressFunc。
//
// 输入：name 卷内路径；done 已传字节；total 文件大小
// 输出：无
// 预期：200ms 节流；done>=total 时打印收尾行并换行
func (u *uploadReporter) report(name string, done, total int64) {
	now := time.Now()
	if name != u.name {
		u.name = name
		u.start = now
		u.last = now
		u.open = false
		return
	}
	final := total > 0 && done >= total
	if !final && now.Sub(u.last) < progressInterval {
		return
	}
	u.render(done, total, final)
}

// render 打印一行上传进度。
//
// 输入：done 已传字节；total 总大小；final 是否结束
// 输出：写到 errw
// 预期：total<=0 时不打印；结束时补换行
func (u *uploadReporter) render(done, total int64, final bool) {
	if total <= 0 {
		return
	}
	pct := float64(done) / float64(total) * 100
	if pct > 100 {
		pct = 100
	}
	if final {
		fmt.Fprintf(u.errw, "\r%s  %s / %s\n", u.name, humanBytes(done), humanBytes(total))
		u.open = false
		return
	}
	var rate int64
	if elapsed := time.Since(u.start).Seconds(); elapsed > 0 {
		rate = int64(float64(done) / elapsed)
	}
	fmt.Fprintf(u.errw, "\r%s  %5.1f%%  %s / %s  %s/s", u.name, pct, humanBytes(done), humanBytes(total), humanBytes(rate))
	u.open = true
}

// end 收尾当前文件：有未换行的进度行时补一个换行。
//
// 输入：无
// 输出：无
// 预期：去重命中不产生进度，此时什么都不做
func (u *uploadReporter) end() {
	if u.open {
		fmt.Fprintln(u.errw)
		u.open = false
	}
}

// humanBytes 把字节数格式化成十进制单位。
//
// 输入：字节数（负数按 0 处理）
// 输出：形如 "999 B"、"1.5 kB"、"2.3 MB"
// 预期：单位用 1000 进制，终端里读起来比纯数字直观
func humanBytes(n int64) string {
	if n <= 0 {
		return "0 B"
	}
	const unit = 1000
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "kMGTPE"[exp])
}
