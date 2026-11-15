package app_test

import (
	"testing"

	"mytgfs/internal/app"
	"mytgfs/internal/config"
)

// 回归背景：建卷/attach 曾只写配置文件不动运行时注册表，新卷要重启才出现在面板；
// 删卷也修过一半。VolumeSync 把「配置 vs 运行时」的差异计算收敛成一个可测的纯函数，
// 两个装配根的 reconcile 都用它，配置变了运行时必须立即跟上。
func TestVolumeSyncMountsNewVolumes(t *testing.T) {
	mount, unmount := app.VolumeSync(
		[]config.Volume{{Name: "demo", ChatID: 1, Enabled: true}},
		nil,
	)
	if len(mount) != 1 || mount[0].Name != "demo" {
		t.Fatalf("mount = %v; want [demo]", mount)
	}
	if len(unmount) != 0 {
		t.Fatalf("unmount = %v; want 空", unmount)
	}
}

func TestVolumeSyncUnmountsRemovedVolumes(t *testing.T) {
	mount, unmount := app.VolumeSync(nil, []string{"demo", "demo2"})
	if len(mount) != 0 {
		t.Fatalf("mount = %v; want 空", mount)
	}
	if len(unmount) != 2 {
		t.Fatalf("unmount = %v; want [demo demo2]", unmount)
	}
}

// 删卷后配置里没有、运行时还在 → 摘除；大小写差异不算新卷
func TestVolumeSyncCaseInsensitiveNames(t *testing.T) {
	mount, unmount := app.VolumeSync(
		[]config.Volume{{Name: "Demo", ChatID: 1, Enabled: true}},
		[]string{"demo"},
	)
	if len(mount) != 0 {
		t.Fatalf("大小写不同不应重复挂载: %v", mount)
	}
	if len(unmount) != 0 {
		t.Fatalf("unmount = %v; want 空", unmount)
	}
}

// 停用的卷视为不存在：运行时残留要摘除
func TestVolumeSyncDisabledMeansUnmount(t *testing.T) {
	mount, unmount := app.VolumeSync(
		[]config.Volume{{Name: "demo", ChatID: 1, Enabled: false}},
		[]string{"demo"},
	)
	if len(mount) != 0 {
		t.Fatalf("mount = %v; want 空", mount)
	}
	if len(unmount) != 1 || unmount[0] != "demo" {
		t.Fatalf("unmount = %v; want [demo]", unmount)
	}
}

// 双向同时差：新建的挂、被删的卸
func TestVolumeSyncBothDirections(t *testing.T) {
	mount, unmount := app.VolumeSync(
		[]config.Volume{
			{Name: "demo", ChatID: 1, Enabled: true},
			{Name: "new", ChatID: 2, Enabled: true},
		},
		[]string{"demo", "old"},
	)
	if len(mount) != 1 || mount[0].Name != "new" {
		t.Fatalf("mount = %v; want [new]", mount)
	}
	if len(unmount) != 1 || unmount[0] != "old" {
		t.Fatalf("unmount = %v; want [old]", unmount)
	}
}
