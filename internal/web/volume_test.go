package web

// 卷封面命名约定与卷名校验的单元测试。

import "testing"

// TestIsCoverName 只认 cover/.cover 加图片扩展名。
//
// 输入：各种文件名
// 输出：是否为保留封面名
// 预期：大小写不敏感；非图片扩展名、stem 不符都返回 false
func TestIsCoverName(t *testing.T) {
	yes := []string{".cover.jpg", ".cover.PNG", "cover.jpeg", "Cover.webp", ".cover.gif"}
	no := []string{"cover.txt", "my-cover.jpg", "cover", ".cover.exe", "folder.jpg", "cover.jpg.bak"}
	for _, n := range yes {
		if !isCoverName(n) {
			t.Errorf("%q 应识别为封面", n)
		}
	}
	for _, n := range no {
		if isCoverName(n) {
			t.Errorf("%q 不应识别为封面", n)
		}
	}
}

// TestValidVolumeName 卷名只允许安全字符。
//
// 输入：合法与非法卷名
// 输出：布尔
// 预期：斜杠、空格、中文、超长都拒绝，避免拼进 index-<name>.db 出问题
func TestValidVolumeName(t *testing.T) {
	for _, n := range []string{"demo", "demo2", "a-b_c", "A1"} {
		if !validVolumeName(n) {
			t.Errorf("%q 应合法", n)
		}
	}
	for _, n := range []string{"", "../x", "a/b", "中文", "a b", string(make([]byte, 40))} {
		if validVolumeName(n) {
			t.Errorf("%q 应非法", n)
		}
	}
}
