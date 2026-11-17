package app

// 删卷流程里与 Telegram 无关的那两段：卷查找与本地缓存清理。

import (
	"os"
	"path/filepath"
	"testing"

	"mytgfs/internal/config"
)

// TestFindConfiguredVolume 验证卷名匹配规则。
//
// 输入：配置里一个卷 sezhijian
// 输出：无；断言失败时 t.Error
// 预期：大小写不敏感、忽略首尾空白；不存在返回 false；不套用隐式卷名
func TestFindConfiguredVolume(t *testing.T) {
	a := &App{cfg: config.Config{Volumes: []config.Volume{{Name: "sezhijian", ChatID: 1}}}}
	for _, in := range []string{"sezhijian", "SezHiJian", "  sezhijian  "} {
		v, ok := a.findConfiguredVolume(in)
		if !ok || v.Name != "sezhijian" {
			t.Fatalf("%q 应匹配到 sezhijian, got %+v ok=%v", in, v, ok)
		}
	}
	for _, in := range []string{"", "mytgfs", "other"} {
		if _, ok := a.findConfiguredVolume(in); ok {
			t.Fatalf("%q 不该匹配到任何卷", in)
		}
	}
}

// TestPurgeVolumeCaches 验证删卷只清目标卷的派生物。
//
// 输入：临时目录里的缩略图目录、两个频道的封面、目标卷索引
// 输出：无；断言失败时 t.Error
// 预期：目标卷缩略图与封面、目标索引全部消失；别的卷/频道不受影响
func TestPurgeVolumeCaches(t *testing.T) {
	t.Chdir(t.TempDir())

	vol := config.Volume{Name: "gone", ChatID: 4356528285}
	writeFile(t, filepath.Join("data", "thumbs", "gone", "a.jpg"))
	writeFile(t, filepath.Join("data", "thumbs", "keep", "b.jpg"))
	writeFile(t, filepath.Join("data", "thumbs", "cover-4356528285-100.jpg"))
	writeFile(t, filepath.Join("data", "thumbs", "cover-4356528285-200.jpg"))
	writeFile(t, filepath.Join("data", "thumbs", "cover-999-300.jpg"))
	writeFile(t, filepath.Join("data", "index-gone.db"))
	writeFile(t, filepath.Join("data", "index-gone.db-wal"))
	writeFile(t, filepath.Join("data", "index-keep.db"))

	a := &App{}
	a.purgeVolumeCaches(vol)

	gone := []string{
		filepath.Join("data", "thumbs", "gone"),
		filepath.Join("data", "thumbs", "cover-4356528285-100.jpg"),
		filepath.Join("data", "thumbs", "cover-4356528285-200.jpg"),
		filepath.Join("data", "index-gone.db"),
		filepath.Join("data", "index-gone.db-wal"),
	}
	for _, p := range gone {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s 应被删除, stat err=%v", p, err)
		}
	}

	kept := []string{
		filepath.Join("data", "thumbs", "keep", "b.jpg"),
		filepath.Join("data", "thumbs", "cover-999-300.jpg"),
		filepath.Join("data", "index-keep.db"),
	}
	for _, p := range kept {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s 不该被删: %v", p, err)
		}
	}
}

// writeFile 造一个父目录已存在或需新建的小文件。
//
// 输入：相对路径；内容固定为 "x"
// 输出：无；失败直接 t.Fatal
func writeFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
