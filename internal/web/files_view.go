package web

// 面板文件列表的展示层：行数据、卷用量、排序、下载/播放地址、面包屑。

import (
	"cmp"
	"html/template"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strings"
	"time"

	"mytgfs/internal/fm"
)

// fileRow 是表格里的一行。
type fileRow struct {
	fm.Entry
	// Path 是卷内绝对路径，供移动/删除使用
	Path string
	// Note 是名字下面的灰色补充（搜索结果里显示所在目录）
	Note string
	// Kind 决定能否内嵌播放：video / audio / pdf / image / 空
	Kind string
	// Download 是已编码好的下载地址。
	// 用 template.URL 是因为 html/template 在 href 上下文会二次转义 %2F → %252F。
	Download template.URL
	// Play 是播放页地址，Kind 非空时才有意义
	Play template.URL
}

// volumeStat 是卷标签上显示的用量。
type volumeStat struct {
	Name   string
	Files  int64
	Used   int64
	Active bool
	// HasStats 为 false 时不显示用量（例如只读模式没有索引）
	HasStats bool
}

// crumb 是面包屑的一段。
type crumb struct {
	Name string
	Path string
}

// browserData 是文件浏览器片段的模板数据。
type browserData struct {
	Labels  pageLabels
	Volumes []string
	// Stats 与 Volumes 一一对应，用于卷标签上的用量
	Stats  []volumeStat
	Volume string
	Dir    string
	Crumbs []crumb
	Rows   []fileRow
	// 搜索与排序：Search 非空表示当前结果是递归搜索结果
	Search string
	Sort   string
	Desc   bool
	// SearchQuery / SortQuery 是拼好的查询串后缀（含前导 &），供排序按钮与翻页复用
	SearchQuery string
	SortQuery   string
	// 分页：Page 从 1 起；Pages<=1 时不渲染翻页条
	Page     int
	Pages    int
	Total    int
	PrevPage int
	NextPage int
	// Message/MsgKind 是上一次操作的反馈，空串表示不显示
	Message string
	MsgKind string
	// Available 为 false 时只显示提示，不渲染工具栏
	Available bool
}

// filesPageSize 是单页最多渲染的条目数。
//
// 目录可能有上千个文件，一次全渲染会让页面与索引查询都很重。
const filesPageSize = 200

// volumeStats 给每个卷取文件数与占用，用于卷标签与汇总。
//
// 输入：names 卷名；active 当前卷
// 输出：与 names 等长的用量列表
// 预期：stats 未注入或某卷取不到时该卷 HasStats=false，不影响其余卷
func (api *filesAPI) volumeStats(names []string, active string) []volumeStat {
	out := make([]volumeStat, 0, len(names))
	for _, n := range names {
		st := volumeStat{Name: n, Active: n == active}
		if api.stats != nil {
			if files, used, ok := api.stats(n); ok {
				st.Files, st.Used, st.HasStats = files, used, true
			}
		}
		out = append(out, st)
	}
	return out
}

// newRow 组装表格行，顺带算好下载地址与可播放类型。
//
// 输入：vol 卷名；p 卷内路径；name 显示名；size/mod 元数据；isDir；note 灰色补充
// 输出：fileRow
// 预期：目录没有下载/播放地址
func newRow(vol, p, name string, size int64, mod time.Time, isDir bool, note string) fileRow {
	row := fileRow{Entry: fm.Entry{Name: name, Dir: isDir, Size: size, Mod: mod}, Path: p, Note: note}
	if isDir {
		return row
	}
	row.Download = downloadURL(vol, p)
	row.Kind = mediaKind(name)
	if row.Kind != "" {
		row.Play = playURL(vol, p)
	}
	return row
}

// mediaKind 按扩展名判断能否在浏览器里直接播。
//
// 输入：文件名
// 输出：video / audio / pdf / image / 空
// 预期：只认浏览器原生支持的常见格式，其余返回空串走下载
func mediaKind(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".mp4", ".webm", ".mkv", ".mov", ".m4v":
		return "video"
	case ".mp3", ".m4a", ".aac", ".ogg", ".opus", ".wav", ".flac":
		return "audio"
	case ".pdf":
		return "pdf"
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp", ".avif":
		return "image"
	}
	return ""
}

// normalizeSort 把排序参数收敛到固定集合。
func normalizeSort(s string) string {
	switch s {
	case "size", "mod":
		return s
	}
	return "name"
}

// sortEntries 原地排序：目录恒在前，其余按指定字段排。
//
// 输入：entries 目录项；key name/size/mod；desc 是否倒序
// 输出：无（原地排序）
// 预期：倒序也不把目录混进文件里
func sortEntries(entries []fm.Entry, key string, desc bool) {
	slices.SortStableFunc(entries, func(a, b fm.Entry) int {
		if a.Dir != b.Dir {
			if a.Dir {
				return -1
			}
			return 1
		}
		if desc {
			a, b = b, a
		}
		switch key {
		case "size":
			if n := cmp.Compare(a.Size, b.Size); n != 0 {
				return n
			}
		case "mod":
			if n := a.Mod.Compare(b.Mod); n != 0 {
				return n
			}
		}
		return cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
}

// downloadURL 拼下载地址。
//
// 输入：vol 卷名；p 卷内路径
// 输出：转义好的 URL
// 预期：
//   - 参数值走 url.Values 百分号编码，中文名和特殊字符都安全
//   - 包成 template.URL 避免模板二次转义 %2F；& 由 html/template 自己转成 &amp;
func downloadURL(vol, p string) template.URL {
	q := url.Values{}
	q.Set("vol", vol)
	q.Set("path", p)
	return template.URL("/files/download?" + q.Encode())
}

// playURL 拼内嵌播放页地址。
//
// 输入：vol 卷名；p 卷内路径
// 输出：转义好的 URL
// 预期：与下载地址同样的编码规则，服务端用 Range 流式读取
func playURL(vol, p string) template.URL {
	q := url.Values{}
	q.Set("vol", vol)
	q.Set("path", p)
	return template.URL("/frag/play?" + q.Encode())
}

// renderBrowser 输出浏览器片段。
//
// 输入：w；data
// 输出：无
// 预期：同时带回工具栏状态（卷、目录），后续操作不会丢上下文
func renderBrowser(w http.ResponseWriter, data browserData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := pageTmpl.ExecuteTemplate(w, "browser", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// crumbsOf 把 /a/b 拆成根 + 各级。
//
// 输入：目录路径
// 输出：面包屑；根名为 "/"
// 预期：路径非法时退化成只有根
func crumbsOf(dir string) []crumb {
	out := []crumb{{Name: "/", Path: "/"}}
	var cur strings.Builder
	for seg := range strings.SplitSeq(strings.Trim(dir, "/"), "/") {
		if seg == "" {
			continue
		}
		cur.WriteString("/" + seg)
		out = append(out, crumb{Name: seg, Path: cur.String()})
	}
	return out
}

// formatMod 把修改时间格式化成表格里的一列。
func formatMod(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return t.Format("2006-01-02 15:04")
}
