package dirent

import (
	"strings"
	"testing"
)

// Test file 消息往返：sha 有值/无值都可取回。
func TestFileRoundTrip(t *testing.T) {
	body, err := EncodeFile("abc123")
	if err != nil {
		t.Fatalf("EncodeFile: %v", err)
	}
	if BodyOf(body) != TypeFile {
		t.Fatalf("BodyOf=%q want %q", BodyOf(body), TypeFile)
	}
	sha, ok := ParseFile(body)
	if !ok || sha != "abc123" {
		t.Fatalf("ParseFile=(%q,%v) want (abc123,true)", sha, ok)
	}

	empty, err := EncodeFile("")
	if err != nil {
		t.Fatalf("EncodeFile empty: %v", err)
	}
	if strings.Contains(empty, `"s"`) {
		t.Fatalf("空 sha 不应写 s 字段: %s", empty)
	}
	if sha, ok := ParseFile(empty); !ok || sha != "" {
		t.Fatalf("ParseFile empty=(%q,%v)", sha, ok)
	}
}

// Test 旧版带 path 的 caption 必须被拒绝，不做兼容。
func TestParseFileRejectsLegacy(t *testing.T) {
	if _, ok := ParseFile(`{"path":"/a.txt","sha256":"x"}`); ok {
		t.Fatal("旧版 caption 应被拒绝")
	}
	if _, ok := ParseFile("not json"); ok {
		t.Fatal("非 JSON 应被拒绝")
	}
	if _, ok := ParseFile(`{"v":1,"t":"f"}`); ok {
		t.Fatal("旧版本号应被拒绝")
	}
}

// Test 目录消息往返：条目、next 均保留。
func TestDirRoundTrip(t *testing.T) {
	entries := []Entry{
		{Name: "a.txt", MsgID: 11},
		{Name: "sub", Dir: true, MsgID: 12},
	}
	body, err := EncodeDir(entries, 0)
	if err != nil {
		t.Fatalf("EncodeDir: %v", err)
	}
	kind, got, next, err := ParseDir(body)
	if err != nil {
		t.Fatalf("ParseDir: %v", err)
	}
	if kind != TypeDir || next != 0 {
		t.Fatalf("kind=%q next=%d", kind, next)
	}
	if len(got) != 2 || got[0] != entries[0] || got[1] != entries[1] {
		t.Fatalf("entries=%+v want %+v", got, entries)
	}
}

// Test 续块往返：type 与 next 都带出去。
func TestChunkRoundTrip(t *testing.T) {
	body, err := EncodeChunk([]Entry{{Name: "b", MsgID: 21}}, 99)
	if err != nil {
		t.Fatalf("EncodeChunk: %v", err)
	}
	kind, got, next, err := ParseDir(body)
	if err != nil {
		t.Fatalf("ParseDir: %v", err)
	}
	if kind != TypeChunk || next != 99 || len(got) != 1 || got[0].Name != "b" {
		t.Fatalf("kind=%q next=%d entries=%+v", kind, next, got)
	}
}

// Test 空目录编码后仍是合法 body，且不带 entries 为 null。
func TestEmptyDir(t *testing.T) {
	body, err := EncodeDir(nil, 0)
	if err != nil {
		t.Fatalf("EncodeDir: %v", err)
	}
	if strings.Contains(body, "null") {
		t.Fatalf("entries 不应为 null: %s", body)
	}
	_, got, _, err := ParseDir(body)
	if err != nil || len(got) != 0 {
		t.Fatalf("ParseDir=(%v,%v)", got, err)
	}
}

// Test ref 替身往返；非法 ref 不算替身。
func TestRefRoundTrip(t *testing.T) {
	body, err := EncodeRef(42)
	if err != nil {
		t.Fatalf("EncodeRef: %v", err)
	}
	id, ok := IsRef(body)
	if !ok || id != 42 {
		t.Fatalf("IsRef=(%d,%v)", id, ok)
	}
	if _, ok := IsRef(`{"ref":0}`); ok {
		t.Fatal("ref<=0 不应算替身")
	}
	if _, ok := IsRef(`{"v":2,"t":"d"}`); ok {
		t.Fatal("目录 body 不应算替身")
	}
}

// Test NeedsRef 阈值边界。
func TestNeedsRef(t *testing.T) {
	if NeedsRef(strings.Repeat("x", RefThreshold)) {
		t.Fatal("刚好等于阈值不应走 ref")
	}
	if !NeedsRef(strings.Repeat("x", RefThreshold+1)) {
		t.Fatal("超过阈值应走 ref")
	}
}

// Test SplitEntries：块数与顺序，且拼接结果与输入一致。
func TestSplitEntries(t *testing.T) {
	entries := make([]Entry, 0, 10)
	for i := 1; i <= 10; i++ {
		entries = append(entries, Entry{Name: strings.Repeat("n", 40), MsgID: i})
	}
	blocks, err := SplitEntries(entries, 200)
	if err != nil {
		t.Fatalf("SplitEntries: %v", err)
	}
	if len(blocks) < 2 {
		t.Fatalf("应切成多块，实际 %d 块", len(blocks))
	}
	var flat []Entry
	for _, b := range blocks {
		flat = append(flat, b...)
		if len(b) == 0 {
			t.Fatal("除空输入外不应出现空块")
		}
	}
	if len(flat) != len(entries) {
		t.Fatalf("拼接 %d 项，输入 %d 项", len(flat), len(entries))
	}
	for i := range flat {
		if flat[i] != entries[i] {
			t.Fatalf("第 %d 项 %+v != %+v", i, flat[i], entries[i])
		}
	}
}

// Test SplitEntries 空输入返回一个空块；单条超预算独占一块。
func TestSplitEntriesEdge(t *testing.T) {
	blocks, err := SplitEntries(nil, 0)
	if err != nil {
		t.Fatalf("SplitEntries nil: %v", err)
	}
	if len(blocks) != 1 || len(blocks[0]) != 0 {
		t.Fatalf("空输入应返回一个空块，实际 %+v", blocks)
	}

	big := []Entry{{Name: strings.Repeat("x", 500), MsgID: 1}, {Name: "s", MsgID: 2}}
	blocks, err = SplitEntries(big, 64)
	if err != nil {
		t.Fatalf("SplitEntries big: %v", err)
	}
	if len(blocks) != 2 || len(blocks[0]) != 1 || blocks[0][0].MsgID != 1 {
		t.Fatalf("超预算条目应独占一块，实际 %+v", blocks)
	}
}

// Test Merge / Drop / Rename 三个目录项编辑操作。
func TestMergeDropRename(t *testing.T) {
	base := []Entry{{Name: "a", MsgID: 1}, {Name: "b", MsgID: 2}}

	merged := Merge(base, []Entry{{Name: "b", MsgID: 20}, {Name: "c", MsgID: 3}})
	if len(merged) != 3 || merged[1].MsgID != 20 || merged[2].Name != "c" {
		t.Fatalf("Merge=%+v", merged)
	}
	if base[1].MsgID != 2 {
		t.Fatal("Merge 不应改入参切片")
	}

	named, deleted := Drop(merged, "c")
	if !deleted || len(named) != 2 {
		t.Fatalf("Drop=(%+v,%v)", named, deleted)
	}
	if _, deleted := Drop(named, "zzz"); deleted {
		t.Fatal("删不存在的名字应返回 false")
	}

	renamed, ok := Rename(named, "a", "a2")
	if !ok || renamed[0].Name != "a2" {
		t.Fatalf("Rename=(%+v,%v)", renamed, ok)
	}
	if _, ok := Rename(named, "a", "b"); ok {
		t.Fatal("目标名已存在时不应改名")
	}
	if _, ok := Rename(named, "zzz", "y"); ok {
		t.Fatal("源名不存在时不应改名")
	}
}

// Test Entry JSON 形状与非法输入。
func TestEntryJSON(t *testing.T) {
	body, err := EncodeDir([]Entry{{Name: "a", Dir: true, MsgID: 7}}, 0)
	if err != nil {
		t.Fatalf("EncodeDir: %v", err)
	}
	if !strings.Contains(body, `["a","d",7]`) {
		t.Fatalf("条目应为紧凑三元组: %s", body)
	}

	var e Entry
	for _, bad := range []string{`["a","x",1]`, `["a","f"]`, `["","f",1]`, `["a","f",0]`, `"s"`} {
		if err := e.UnmarshalJSON([]byte(bad)); err == nil {
			t.Fatalf("%s 应被拒绝", bad)
		}
	}
}

// Test BodyOf 分类：ref 与旧格式返回空串。
func TestBodyOf(t *testing.T) {
	dir, _ := EncodeDir(nil, 0)
	chunk, _ := EncodeChunk(nil, 0)
	file, _ := EncodeFile("")
	ref, _ := EncodeRef(5)
	cases := map[string]string{
		dir:                   TypeDir,
		chunk:                 TypeChunk,
		file:                  TypeFile,
		ref:                   "",
		`{"path":"/a"}`:       "",
		"":                    "",
		`{"v":2,"t":"weird"}`: "",
	}
	for body, want := range cases {
		if got := BodyOf(body); got != want {
			t.Fatalf("BodyOf(%q)=%q want %q", body, got, want)
		}
	}
}
