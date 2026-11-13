package web

// 页面模板集合：整页与「文件浏览器」片段共用同一个 template.Template，
// 这样整页里的 {{template "browser"}} 和 htmx 片段渲染用的是同一份定义。

import "html/template"

// pageTmpl 是整页 + 片段模板集合。
var pageTmpl = buildTemplates()

func buildTemplates() *template.Template {
	t := template.New("page").Funcs(tmplFuncs)
	template.Must(t.Parse(pageHTML))
	// 片段模板单独命名，执行时用 ExecuteTemplate(w, "browser", data)
	template.Must(t.New("browser").Funcs(tmplFuncs).Parse(browserHTML))
	template.Must(t.New("mountbox").Funcs(tmplFuncs).Parse(mountboxHTML))
	template.Must(t.New("details").Funcs(tmplFuncs).Parse(detailsHTML))
	template.Must(t.New("play").Funcs(tmplFuncs).Parse(playHTML))
	return t
}

// browserHTML 是文件浏览器的片段内容（#browser 的 innerHTML）。
//
// 输入：browserData
// 输出：工具栏 + 面包屑 + 表格
// 预期：
//   - 每个表单都带上 vol/dir 隐藏字段，片段替换后上下文不丢
//   - 操作失败也返回这段内容，htmx 就地替换
const browserHTML = `
{{if .Message}}<p class="msg"><span class="{{.MsgKind}}">{{.Message}}</span></p>{{end}}
{{if .Available}}
<div class="vtabs">
  {{range .Stats}}<button type="button" class="ghost{{if .Active}} on{{end}}" hx-get="/frag/files?vol={{urlq .Name}}&dir=%2F" hx-target="#browser" hx-swap="innerHTML">
    <span class="vname">{{.Name}}</span>{{if .HasStats}}<span class="vmeta">{{$.Labels.Entries}} {{.Files}} · {{bytes .Used}}</span>{{end}}
  </button>{{end}}
</div>
<div class="fbar">
  <nav class="crumbs">
    {{range .Crumbs}}<a hx-get="/frag/files?vol={{urlq $.Volume}}&dir={{urlq .Path}}" hx-target="#browser" hx-swap="innerHTML">{{.Name}}</a>{{end}}
  </nav>
  <form class="fsearch" hx-get="/frag/files" hx-target="#browser" hx-swap="innerHTML">
    <input type="hidden" name="vol" value="{{.Volume}}">
    <input type="hidden" name="dir" value="{{.Dir}}">
    <input type="hidden" name="sort" value="{{.Sort}}">
    {{if .Desc}}<input type="hidden" name="desc" value="1">{{end}}
    <input name="q" value="{{.Search}}" placeholder="{{.Labels.FilesSearch}}" autocomplete="off">
    <button type="submit" class="ghost">{{.Labels.FilesSearch}}</button>
  </form>
</div>
<div class="fbar">
  <form class="fup" data-upload hx-post="/frag/files/upload" hx-encoding="multipart/form-data"
        hx-target="#browser" hx-swap="innerHTML">
    <input type="hidden" name="vol" value="{{.Volume}}">
    <input type="hidden" name="dir" value="{{.Dir}}">
    <input type="file" name="file" multiple required data-uploader>
    <input type="file" name="dirfile" webkitdirectory directory multiple data-dirpick class="hidden">
    <button type="submit" data-plain-upload>{{.Labels.FilesUpload}}</button>
    <button type="button" class="ghost" data-dirbtn>{{.Labels.FilesDirUpload}}</button>
    <span class="upwrap" data-progress-wrap hidden><i data-progress-bar></i></span>
    <span class="msg" data-progress data-progress-label="{{.Labels.FilesUploading}}"></span>
  </form>
  <form class="fmk" hx-post="/frag/files/mkdir" hx-target="#browser" hx-swap="innerHTML">
    <input type="hidden" name="vol" value="{{.Volume}}">
    <input type="hidden" name="dir" value="{{.Dir}}">
    <input name="name" autocomplete="off" placeholder="{{.Labels.FilesNewDir}}" required>
    <button type="submit">{{.Labels.FilesMkdir}}</button>
  </form>
</div>
<div class="fbar fsorts">
  <span class="flabel">{{.Labels.FilesSortName}}</span>
  <button type="button" class="ghost{{if eq .Sort "name"}} on{{end}}" hx-get="/frag/files?vol={{urlq .Volume}}&dir={{urlq .Dir}}&sort=name{{if .Desc}}&desc=1{{end}}{{.SearchQuery}}" hx-target="#browser" hx-swap="innerHTML">{{.Labels.FilesName}}</button>
  <button type="button" class="ghost{{if eq .Sort "size"}} on{{end}}" hx-get="/frag/files?vol={{urlq .Volume}}&dir={{urlq .Dir}}&sort=size{{if .Desc}}&desc=1{{end}}{{.SearchQuery}}" hx-target="#browser" hx-swap="innerHTML">{{.Labels.FilesSize}}</button>
  <button type="button" class="ghost{{if eq .Sort "mod"}} on{{end}}" hx-get="/frag/files?vol={{urlq .Volume}}&dir={{urlq .Dir}}&sort=mod{{if .Desc}}&desc=1{{end}}{{.SearchQuery}}" hx-target="#browser" hx-swap="innerHTML">{{.Labels.FilesMtime}}</button>
  <button type="button" class="ghost{{if .Desc}} on{{end}}" hx-get="/frag/files?vol={{urlq .Volume}}&dir={{urlq .Dir}}&sort={{.Sort}}{{if not .Desc}}&desc=1{{end}}{{.SearchQuery}}" hx-target="#browser" hx-swap="innerHTML">{{.Labels.FilesSortDesc}}</button>
</div>
<form id="batchform" class="fbatch" hx-post="/frag/files/batch" hx-target="#browser" hx-swap="innerHTML">
  <input type="hidden" name="vol" value="{{.Volume}}">
  <input type="hidden" name="dir" value="{{.Dir}}">
  <label class="chk"><input type="checkbox" data-checkall> {{.Labels.FilesSelectAll}}</label>
  <select name="action" aria-label="{{.Labels.FilesBatchGo}}">
    <option value="delete">{{.Labels.FilesBatchDelete}}</option>
    <option value="move">{{.Labels.FilesBatchMoveTo}}</option>
  </select>
  <input name="to" placeholder="{{.Labels.FilesPath}}" autocomplete="off">
  <button type="submit" class="danger">{{.Labels.FilesBatchGo}}</button>
</form>
<div class="fwrap" data-drop>
  <div class="dropzone" data-dropzone hidden>{{.Labels.FilesDrop}}</div>
  <table class="ftable">
    <thead>
      <tr>
        <th class="pick"></th>
        <th>{{.Labels.FilesName}}</th>
        <th class="num">{{.Labels.FilesSize}}</th>
        <th class="num">{{.Labels.FilesMtime}}</th>
        <th></th>
      </tr>
    </thead>
    <tbody>
    {{range .Rows}}
      <tr>
        <td class="pick"><input type="checkbox" name="paths" value="{{.Path}}" form="batchform" aria-label="{{.Name}}"></td>
        <td class="fname">
          {{if .Dir}}<svg class="ic" viewBox="0 0 24 24" aria-hidden="true"><path d="M20 20a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.9a2 2 0 0 1-1.69-.9L9.6 3.9A2 2 0 0 0 7.93 3H4a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2Z"/></svg>{{else}}<svg class="ic" viewBox="0 0 24 24" aria-hidden="true"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8Z"/><path d="M14 2v6h6"/></svg>{{end}}
          {{if .Dir}}<a hx-get="/frag/files?vol={{urlq $.Volume}}&dir={{urlq .Path}}" hx-target="#browser" hx-swap="innerHTML">{{.Name}}</a>{{else}}{{.Name}}{{end}}
          {{if .Note}}<span class="fnote mono">{{.Note}}</span>{{end}}
        </td>
        <td class="num">{{if not .Dir}}{{bytes .Size}}{{end}}</td>
        <td class="num">{{fmtTime .Mod}}</td>
        <td class="acts">
          {{if .Play}}<button type="button" class="ghost" hx-get="{{.Play}}" hx-target="#player" hx-swap="innerHTML">{{$.Labels.FilesPlay}}</button>{{end}}
          {{if not .Dir}}<button type="button" class="ghost" hx-get="/frag/details?vol={{urlq $.Volume}}&path={{urlq .Path}}" hx-target="#detail" hx-swap="innerHTML">{{$.Labels.FilesDetails}}</button>{{end}}
          {{if not .Dir}}<a class="btn" href="{{.Download}}" download>{{$.Labels.FilesDownload}}</a>{{end}}
          <details class="menu">
            <summary>{{$.Labels.FilesMove}}</summary>
            <form hx-post="/frag/files/move" hx-target="#browser" hx-swap="innerHTML">
              <input type="hidden" name="vol" value="{{$.Volume}}">
              <input type="hidden" name="dir" value="{{$.Dir}}">
              <input type="hidden" name="from" value="{{.Path}}">
              <input name="to" value="{{.Path}}" autocomplete="off" aria-label="{{$.Labels.FilesMove}}">
              <button type="submit">{{$.Labels.FilesDone}}</button>
            </form>
          </details>
          <form class="finline" hx-post="/frag/files/remove" hx-target="#browser" hx-swap="innerHTML"
                hx-confirm="{{$.Labels.FilesConfirm}}">
            <input type="hidden" name="vol" value="{{$.Volume}}">
            <input type="hidden" name="dir" value="{{$.Dir}}">
            <input type="hidden" name="path" value="{{.Path}}">
            <button type="submit" class="danger">{{$.Labels.FilesDelete}}</button>
          </form>
        </td>
      </tr>
    {{else}}
      <tr><td colspan="5" class="empty">{{.Labels.FilesEmpty}}</td></tr>
    {{end}}
    </tbody>
  </table>
</div>
{{if gt .Pages 1}}
<div class="pager">
  <button type="button" class="ghost"{{if .PrevPage}} hx-get="/frag/files?vol={{urlq .Volume}}&dir={{urlq .Dir}}&page={{.PrevPage}}{{.SortQuery}}" hx-target="#browser" hx-swap="innerHTML"{{else}} disabled{{end}}>{{.Labels.FilesPrev}}</button>
  <span class="pinfo">{{.Page}} / {{.Pages}} · {{.Total}}</span>
  <button type="button" class="ghost"{{if .NextPage}} hx-get="/frag/files?vol={{urlq .Volume}}&dir={{urlq .Dir}}&page={{.NextPage}}{{.SortQuery}}" hx-target="#browser" hx-swap="innerHTML"{{else}} disabled{{end}}>{{.Labels.FilesNext}}</button>
</div>
{{end}}
<div id="detail" class="detailwrap"></div>
{{end}}`

// playHTML 是播放浮层片段（#player 的 innerHTML）。
//
// 输入：playData
// 输出：标题栏 + 原生 video/audio/iframe/img 播放器
// 预期：
//   - 媒体地址带 Range，边下边播，不整份下载
//   - 关闭按钮只清空浮层，不刷新列表
const playHTML = `
<div class="playbox">
  <div class="playhead">
    <span class="name">{{.Name}}</span>
    <span class="meta">{{.Size}}</span>
    {{if not .Missing}}<a class="btn" href="{{.Download}}" download>{{.Labels.FilesDownload}}</a>{{end}}
    <button type="button" class="ghost" data-close-player>{{.Labels.FilesClose}}</button>
  </div>
  {{if .Missing}}
  <p class="msg"><span class="err">{{.Msg}}</span></p>
  {{else if eq .Kind "video"}}
  <video controls autoplay preload="metadata" src="{{.Src}}"></video>
  {{else if eq .Kind "audio"}}
  <audio controls autoplay preload="metadata" src="{{.Src}}"></audio>
  {{else if eq .Kind "pdf"}}
  <iframe class="pdframe" src="{{.Src}}" title="{{.Name}}"></iframe>
  {{else if eq .Kind "image"}}
  <img class="playimg" src="{{.Src}}" alt="{{.Name}}">
  {{else}}
  <p class="msg">{{.Labels.FilesEmpty}}</p>
  {{end}}
</div>`

// mountboxHTML 是「挂载为网络驱动器」开关片段（#mountbox 的 innerHTML）。
//
// 输入：mountData
// 输出：说明 + 一个开关按钮 + 当前监听地址与卷名
// 预期：
//   - 表单带隐藏字段 on，值为当前状态取反，提交后由服务端确认新状态
//   - 不可用时只显示提示，不渲染按钮
const mountboxHTML = `
{{if not .Available}}
<p class="msg"><span class="err">{{.Labels.MountUnavailable}}</span></p>
{{else}}
<p class="msg">{{.Labels.MountHint}}</p>
<form hx-post="/frag/mount" hx-target="#mountbox" hx-swap="innerHTML">
  <div class="row">
    <input type="hidden" name="on" value="{{if .Enabled}}0{{else}}1{{end}}">
    <button type="submit"{{if .Enabled}} class="ghost"{{end}}>
      <svg class="ic" viewBox="0 0 24 24" aria-hidden="true">
        {{if .Enabled}}<path d="M18 6 6 18M6 6l12 12"/>{{else}}<path d="M12 5v14M5 12h14"/>{{end}}
      </svg>
      <span>{{if .Enabled}}{{.Labels.MountOff}}{{else}}{{.Labels.MountOn}}{{end}}</span>
    </button>
      <span class="msg">{{.Labels.MountAddr}}: <code id="davaddr">{{if .Addr}}{{.Addr}}{{else}}—{{end}}</code>{{with .Volume}} · {{.}}{{end}}</span>
  </div>
</form>
{{if .Message}}<p class="msg"><span class="{{.MsgKind}}">{{.Message}}</span></p>{{end}}
{{end}}`

// detailsHTML 是文件详情片段（#detail 的 innerHTML）。
//
// 输入：detailsData
// 输出：图片缩略图（仅图片）+ 名称/路径/大小/修改时间
// 预期：缩略图只在用户点「详情」后由浏览器请求一次，列表页不预取
const detailsHTML = `
{{if .Message}}
<p class="msg"><span class="err">{{.Message}}</span></p>
{{else}}
<div class="detail">
  {{if .IsImage}}<img class="dthumb" src="{{.Thumb}}" alt="{{.Name}}" loading="lazy" decoding="async">{{end}}
  <dl class="dmeta">
    <div><dt>{{.Labels.FilesName}}</dt><dd>{{.Name}}</dd></div>
    <div><dt>{{.Labels.FilesPath}}</dt><dd class="mono">{{.Path}}</dd></div>
    <div><dt>{{.Labels.FilesSize}}</dt><dd>{{.Size}}</dd></div>
    <div><dt>{{.Labels.FilesMtime}}</dt><dd>{{.Mod}}</dd></div>
  </dl>
</div>
{{end}}`
