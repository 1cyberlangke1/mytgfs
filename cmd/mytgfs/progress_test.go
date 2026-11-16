package main

// 下载进度的单测：字节格式化与节流写行为。

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// TestHumanBytes 十进制单位分档。
func TestHumanBytes(t *testing.T) {
	cases := map[int64]string{
		0:       "0 B",
		-5:      "0 B",
		999:     "999 B",
		1000:    "1.0 kB",
		1500:    "1.5 kB",
		2500000: "2.5 MB",
	}
	for in, want := range cases {
		if got := humanBytes(in); got != want {
			t.Errorf("humanBytes(%d) = %q，期望 %q", in, got, want)
		}
	}
}

// TestProgressWriterThrottle 间隔不足时不刷新，旧记录超过间隔后刷新一次。
func TestProgressWriterThrottle(t *testing.T) {
	var dst, errw bytes.Buffer
	p := newProgressWriter(&dst, &errw, 1000, 0, true)
	if _, err := p.Write([]byte("abc")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if errw.Len() != 0 {
		t.Fatalf("200ms 内不应刷新进度，得到 %q", errw.String())
	}
	// 把上次刷新时间推到很久以前，下一次写入必定刷新
	p.last = time.Now().Add(-time.Second)
	if _, err := p.Write([]byte("de")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	line := errw.String()
	if !strings.Contains(line, "0.5%") || !strings.Contains(line, "5 B / 1.0 kB") {
		t.Fatalf("进度行 = %q", line)
	}
	if dst.String() != "abcde" {
		t.Fatalf("目标内容 = %q", dst.String())
	}
}

// TestProgressWriterDisabled enabled=false 时完全不写 errw。
func TestProgressWriterDisabled(t *testing.T) {
	var dst, errw bytes.Buffer
	p := newProgressWriter(&dst, &errw, 100, 0, false)
	p.last = time.Now().Add(-time.Hour)
	if _, err := p.Write([]byte("xy")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	p.render(true)
	if errw.Len() != 0 {
		t.Fatalf("禁用时不应输出，得到 %q", errw.String())
	}
}

// TestProgressWriterResume 续传时百分比从已存在字节开始算。
func TestProgressWriterResume(t *testing.T) {
	var dst, errw bytes.Buffer
	p := newProgressWriter(&dst, &errw, 100, 40, true)
	p.last = time.Now().Add(-time.Second)
	if _, err := p.Write([]byte("1234567890")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if !strings.Contains(errw.String(), "50.0%") {
		t.Fatalf("续传进度 = %q，期望 50.0%%", errw.String())
	}
}
