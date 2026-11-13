package main

// cp 的跳过判定单测：只有大小一致且 sha256 非空并相同才算同内容。

import (
	"testing"

	"mytgfs/internal/index"
)

// TestSameContent 覆盖跳过判定的全部分支。
//
// 输入：构造好的两条索引记录
// 输出：无
// 预期：sha256 任一为空都返回 false，避免把重建后的空 sha 误判成相同
func TestSameContent(t *testing.T) {
	src := index.File{Size: 27, Sha256: "abc"}
	cases := []struct {
		name string
		dst  index.File
		src  index.File
		want bool
	}{
		{"大小与 sha 都相同", index.File{Size: 27, Sha256: "abc"}, src, true},
		{"sha 不同", index.File{Size: 27, Sha256: "def"}, src, false},
		{"大小不同", index.File{Size: 28, Sha256: "abc"}, src, false},
		{"目标 sha 为空", index.File{Size: 27}, src, false},
		{"源 sha 为空", index.File{Size: 27, Sha256: "abc"}, index.File{Size: 27}, false},
		{"双方 sha 都为空", index.File{Size: 27}, index.File{Size: 27}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := sameContent(c.dst, c.src); got != c.want {
				t.Fatalf("sameContent() = %v, want %v", got, c.want)
			}
		})
	}
}
