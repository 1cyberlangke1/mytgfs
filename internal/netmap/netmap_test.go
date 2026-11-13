package netmap

// 映射盘符解析的单测：不真正调用 net use，只验证匹配逻辑。

import (
	"slices"
	"testing"
)

// TestMappingDrivesMatchesBothForms 验证 host:port 与 host@port 两种写法都能命中。
//
// 输入：一段仿 net use 输出，含两种写法各一行
// 输出：两个盘符，按出现顺序
func TestMappingDrivesMatchesBothForms(t *testing.T) {
	out := "OK           Y:        \\\\127.0.0.1@8080\\DavWWWRoot     Microsoft Windows Network\r\n" +
		"OK           Z:        \\\\127.0.0.1:8080\\DavWWWRoot     Microsoft Windows Network\r\n"
	got := mappingDrives(out, "127.0.0.1:8080")
	want := []string{"Y:", "Z:"}
	if !slices.Equal(got, want) {
		t.Fatalf("盘符 = %v，期望 %v", got, want)
	}
}

// TestMappingDrivesIgnoresOtherAddresses 其它地址与本地盘不能误删。
//
// 输入：输出里混入别的服务地址和本地盘挂载
// 输出：只为目标地址返回盘符
func TestMappingDrivesIgnoresOtherAddresses(t *testing.T) {
	out := "OK           C:        \\\\Server\\Share        Microsoft Windows Network\r\n" +
		"OK           Y:        \\\\127.0.0.1@9999\\DavWWWRoot  Microsoft Windows Network\r\n" +
		"OK           X:        \\\\127.0.0.1@8080\\DavWWWRoot  Microsoft Windows Network\r\n"
	got := mappingDrives(out, "127.0.0.1:8080")
	if !slices.Equal(got, []string{"X:"}) {
		t.Fatalf("盘符 = %v，期望 [X:]", got)
	}
}

// TestMappingDrivesEmptyAddr 空地址一律不删，避免误伤全部映射。
func TestMappingDrivesEmptyAddr(t *testing.T) {
	out := "OK           Y:        \\\\127.0.0.1@8080\\DavWWWRoot\r\n"
	if got := mappingDrives(out, ""); len(got) != 0 {
		t.Fatalf("盘符 = %v，期望空", got)
	}
}

// TestMappingDrivesNoMatch 没有匹配行时返回空。
func TestMappingDrivesNoMatch(t *testing.T) {
	out := "There are no entries in the list.\r\n"
	if got := mappingDrives(out, "127.0.0.1:8080"); len(got) != 0 {
		t.Fatalf("盘符 = %v，期望空", got)
	}
}

// TestCleanupEmptyAddr 空地址不 panic，也不该去跑 net use。
func TestCleanupEmptyAddr(t *testing.T) {
	Cleanup("") // 不 panic 即通过
}
