package dav

import "time"

// 上传临时文件的命名与保留时长。
//
// 异常中断（关标签页、拔网线、杀进程）时 Close 不会被调用，
// 临时文件只能靠下次上传时按时间清理。
const (
	tempPattern = ".mytgfs-dav-*"
	tempMaxAge  = 24 * time.Hour
)

// nowUnix 返回当前 Unix 秒。
func nowUnix() int64 { return time.Now().Unix() }

// unixTime 把 Unix 秒转成 time.Time。
func unixTime(v int64) time.Time { return time.Unix(v, 0) }
