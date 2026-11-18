package auth

// 验证码读取：同一个 bufio.Reader 连续读多行时不能丢缓冲。

import (
	"bufio"
	"context"
	"strings"
	"testing"
)

// TestCodeReaderKeepsBufferedLines 两次验证码读取能拿到管道里的两行。
//
// 输入：同一个 Reader，内容 "111111\n222222\n"
// 输出：第一次 111111，第二次 222222
// 预期：若每次重试都新建 Reader，第二次会读到空/卡住
func TestCodeReaderKeepsBufferedLines(t *testing.T) {
	stdin := bufio.NewReader(strings.NewReader("111111\n222222\n"))
	read := codeReader(stdin)
	ctx := context.Background()

	first, err := read(ctx, nil)
	if err != nil || first != "111111" {
		t.Fatalf("第一次 = %q, %v", first, err)
	}
	second, err := read(ctx, nil)
	if err != nil || second != "222222" {
		t.Fatalf("第二次 = %q, %v（缓冲被丢弃？）", second, err)
	}
}

// TestCodeReaderTrimsWhitespace 行尾空白与回车不进入验证码。
func TestCodeReaderTrimsWhitespace(t *testing.T) {
	stdin := bufio.NewReader(strings.NewReader("  333333 \r\n"))
	got, err := codeReader(stdin)(context.Background(), nil)
	if err != nil || got != "333333" {
		t.Fatalf("got=%q err=%v", got, err)
	}
}
