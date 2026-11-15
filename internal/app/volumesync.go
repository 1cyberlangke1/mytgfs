package app

import (
	"strings"

	"mytgfs/internal/config"
)

// VolumeSync 计算运行时卷表与配置文件的差异。
//
// 输入：vols 配置里的卷（Enabled=false 视为不存在）；mounted 当前已挂载的卷名（大小写不敏感）
// 输出：mount 需要新挂载的卷（保持配置原样）；unmount 需要摘除的卷名（运行时原名）
// 预期：建卷/attach/删卷等一切配置变更都以「调它 + 应用结果 + 推送」收尾，
// 配置与运行时由此耦合，用户不需要重启
func VolumeSync(vols []config.Volume, mounted []string) (mount []config.Volume, unmount []string) {
	want := make(map[string]bool, len(vols))
	for _, v := range vols {
		if v.Enabled {
			want[strings.ToLower(v.Name)] = true
		}
	}
	mountedSet := make(map[string]bool, len(mounted))
	for _, name := range mounted {
		mountedSet[strings.ToLower(name)] = true
	}
	for _, v := range vols {
		if v.Enabled && !mountedSet[strings.ToLower(v.Name)] {
			mount = append(mount, v)
		}
	}
	for _, name := range mounted {
		if !want[strings.ToLower(name)] {
			unmount = append(unmount, name)
		}
	}
	return mount, unmount
}
