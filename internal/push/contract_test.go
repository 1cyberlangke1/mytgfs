package push

// 架构约束测试：传输通道只能由 push 包持有。
//
// 规则：
//   - `internal/push` 以外的代码不许 import `internal/eventbus`
//   - 不许自己调用 PublishEvery（节流与尾沿只在 Hub 里实现一次）
//
// 于是「忘推送」「私开一条通道」「各处节流参数不一致」都会在 go test 里失败，
// 而不是靠人记住。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// allowedDirs 是允许直接触碰总线方法的目录（相对仓库根）。
var allowedDirs = []string{
	filepath.Join("internal", "push"),
	filepath.Join("internal", "eventbus"),
}

// skipDirs 是不参与扫描的目录。
var skipDirs = map[string]bool{
	".git": true, "tmp": true, "data": true, "build": true,
	"node_modules": true, "dist": true, ".venv": true,
}

// TestNoDirectPublish 扫描全仓 Go 源码，禁止绕过 push 包直接推送。
//
// 输入：仓库根目录下的所有 .go 文件（排除本包与 eventbus）
// 输出：发现 Publish/PublishEvery 调用即失败并给出位置
// 预期：新增写路径只能通过 push.Events 推送
func TestNoDirectPublish(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	var violations []string
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		for _, allowed := range allowedDirs {
			if strings.HasPrefix(rel, allowed+string(filepath.Separator)) {
				return nil
			}
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := string(data)
		for _, needle := range []string{`"mytgfs/internal/eventbus"`, ".PublishEvery("} {
			if i := strings.Index(text, needle); i >= 0 {
				line := 1 + strings.Count(text[:i], "\n")
				violations = append(violations, rel+":"+itoa(line)+" 直接使用 "+needle)
				break
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) > 0 {
		t.Fatalf("推送必须经由 push.Events：\n%s", strings.Join(violations, "\n"))
	}
}

// itoa 是最小整数转字符串，避免为一条测试引入 import。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
