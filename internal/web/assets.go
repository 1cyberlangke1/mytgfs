package web

// 内嵌前端资源：整页 HTML 与静态脚本。
//
// 输入：无（编译期 embed）
// 输出：pageHTML 常量与 htmxJS / appJS 字节串
// 预期：全离线可用，不依赖任何 CDN

import _ "embed"

//go:embed static/htmx.min.js
var htmxJS []byte

//go:embed static/app.js
var appJS []byte

const pageHTML = `<!doctype html>
<html lang="{{.Labels.Lang}}" data-theme="dark">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<meta name="color-scheme" content="dark light">
<title>mytgfs</title>
{{if .PWA.Enabled}}<link rel="manifest" href="/manifest.webmanifest">
<meta name="theme-color" content="#0b1220">
<link rel="apple-touch-icon" href="/static/apple-touch-icon.png">
<meta name="apple-mobile-web-app-capable" content="yes">
<meta name="mobile-web-app-capable" content="yes">
<meta name="apple-mobile-web-app-title" content="{{.PWA.Name}}">
<meta name="application-name" content="{{.PWA.Name}}">
<link rel="icon" type="image/png" href="/static/icon-192.png">
{{end}}
<script>
// 首屏前定主题：手动选择 > 系统偏好 > 深色。避免亮暗闪烁。
(function(){
  try {
    var t = localStorage.getItem('mytgfs-theme');
    if (t !== 'light' && t !== 'dark') {
      t = window.matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark';
    }
    document.documentElement.dataset.theme = t;
  } catch (e) { document.documentElement.dataset.theme = 'dark'; }
})();
</script>
<style>
:root{
  --bg0:#0b1220; --bg1:#101b30;
  --glow1:#17406a; --glow2:#123a57;
  --card:rgba(255,255,255,.055);
  --line:rgba(255,255,255,.10);
  --line-strong:rgba(255,255,255,.20);
  --text:#e8eefb; --muted:#8ea0bf;
  --blue:#2aa7e0; --blue-deep:#1b86bb;
  --green:#39d98a; --amber:#f5b94c; --red:#ff6b7a;
  --chip:rgba(255,255,255,.05);
  --btn-ghost:rgba(255,255,255,.06);
  --input:rgba(0,0,0,.28);
  --stat:rgba(255,255,255,.04);
  --shadow:0 18px 50px rgba(0,0,0,.25);
  --banner-bg:rgba(255,107,122,.12);
  --banner-line:rgba(255,107,122,.42);
  --banner-text:#ffd7db;
}
:root[data-theme="light"]{
  --bg0:#f7f9fc; --bg1:#eef2f9;
  --glow1:#cfe4f7; --glow2:#dde9fb;
  --card:#ffffff;
  --line:rgba(16,32,52,.10);
  --line-strong:rgba(16,32,52,.20);
  --text:#16202e; --muted:#5b6b80;
  --blue:#1b86bb; --blue-deep:#146a95;
  --green:#0f9d5b; --amber:#b7791f; --red:#d93a4b;
  --chip:rgba(16,32,52,.045);
  --btn-ghost:rgba(16,32,52,.05);
  --input:#f4f7fc;
  --stat:#f7f9fd;
  --shadow:0 14px 36px rgba(23,43,77,.10);
  --banner-bg:rgba(217,58,75,.08);
  --banner-line:rgba(217,58,75,.32);
  --banner-text:#a61f2e;
}
*{box-sizing:border-box}
html,body{height:100%}
body{
  margin:0; color:var(--text); font-family:system-ui,-apple-system,"Segoe UI",Roboto,"Noto Sans SC",sans-serif;
  background:
    radial-gradient(1200px 700px at 8% -10%, var(--glow1) 0%, transparent 55%),
    radial-gradient(900px 600px at 110% 10%, var(--glow2) 0%, transparent 50%),
    linear-gradient(160deg,var(--bg0),var(--bg1));
  min-height:100vh; padding:28px 18px 48px;
  transition:background-color .2s ease, color .2s ease;
}
.wrap{max-width:1000px;margin:0 auto}
header{display:flex;align-items:center;gap:14px;margin-bottom:22px}
header .logo{width:54px;height:54px;flex:none;filter:drop-shadow(0 8px 18px rgba(42,167,224,.35))}
header h1{margin:0;font-size:21px;letter-spacing:.2px}
header .sub{margin:2px 0 0;color:var(--muted);font-size:13px}
.pill{padding:7px 14px;border-radius:999px;font-size:13px;font-weight:600;
  border:1px solid var(--line); background:var(--chip); white-space:nowrap}
.pill.ok{color:var(--green);border-color:rgba(57,217,138,.4);background:rgba(57,217,138,.10)}
.pill.warn{color:var(--amber);border-color:rgba(245,185,76,.4);background:rgba(245,185,76,.10)}
.pill.bad{color:var(--red);border-color:rgba(255,107,122,.4);background:rgba(255,107,122,.10)}
.pill.ok::before,.pill.warn::before,.pill.bad::before{
  content:"";display:inline-block;width:7px;height:7px;border-radius:50%;
  background:currentColor;margin-right:8px;vertical-align:1px;
  box-shadow:0 0 10px currentColor;animation:breathe 2.4s ease-in-out infinite;
}
@keyframes breathe{50%{opacity:.35}}
.head-actions{margin-left:auto;display:flex;align-items:center;gap:10px}
main{display:grid;gap:16px;grid-template-columns:1fr;align-items:start}
@media(min-width:860px){main{grid-template-columns:1.15fr .85fr}}
/* 窄屏（手机 / 竖屏平板）：单列、按钮铺满、避免横向溢出 */
@media(max-width:640px){
  body{padding:18px 12px 36px}
  header{flex-wrap:wrap;gap:10px;margin-bottom:16px}
  header .logo{width:44px;height:44px}
  header h1{font-size:18px}
  header .sub{font-size:12px}
  .head-actions{margin-left:0;width:100%;justify-content:flex-end}
  .card{padding:14px;border-radius:14px}
  .vol{flex-wrap:wrap;align-items:flex-start}
  .vol .meta{margin-left:0;width:100%;text-align:left}
  .card button:not(.iconbtn){width:100%;justify-content:center}
  .form{grid-template-columns:1fr}
  textarea{min-height:140px}
  .row{gap:8px}
}
/* 超窄屏：状态条与标题各占一行 */
@media(max-width:400px){
  .head-actions{justify-content:space-between}
  .pill{padding:6px 10px;font-size:12px}
  .stats{grid-template-columns:1fr}
}
.card{background:var(--card);border:1px solid var(--line);border-radius:16px;padding:18px 18px 16px;
  box-shadow:var(--shadow);transition:border-color .18s ease, transform .18s ease, background-color .2s ease}
.card:hover{border-color:var(--line-strong);transform:translateY(-1px)}
.card h2{margin:0 0 14px;font-size:13px;font-weight:600;color:var(--muted);letter-spacing:.4px;text-transform:uppercase;
  display:flex;align-items:center;gap:8px}
.vol{display:flex;align-items:center;gap:12px;padding:12px 0;border-top:1px solid var(--line)}
.vol:first-of-type{border-top:0;padding-top:0}
.vol .dot{width:9px;height:9px;border-radius:50%;background:var(--green);box-shadow:0 0 12px var(--green);flex:none}
.vol .dot.busy{background:var(--blue);box-shadow:0 0 12px var(--blue);animation:breathe 1.2s ease-in-out infinite}
.vol .name{font-weight:600;font-size:15px}
.vol .meta{margin-left:auto;color:var(--muted);font-size:13px;text-align:right;line-height:1.5;font-variant-numeric:tabular-nums}
.empty{color:var(--muted);font-size:14px;padding:6px 0}
.stats{display:grid;grid-template-columns:repeat(auto-fit,minmax(110px,1fr));gap:10px;margin-bottom:16px}
.stat{background:var(--stat);border:1px solid var(--line);border-radius:12px;padding:10px 12px}
.stat .k{display:block;color:var(--muted);font-size:11px;letter-spacing:.4px;margin-bottom:4px}
.stat strong{font-size:17px;font-weight:650;font-variant-numeric:tabular-nums}
button{
  appearance:none;border:1px solid rgba(42,167,224,.45);background:linear-gradient(180deg,var(--blue),var(--blue-deep));
  color:#fff;font:inherit;font-weight:600;padding:10px 16px;border-radius:11px;cursor:pointer;
  transition:transform .06s ease, filter .15s ease, background-color .2s ease;
  display:inline-flex;align-items:center;gap:8px;
}
button:hover{filter:brightness(1.08)}
button:active{transform:translateY(1px)}
button:focus-visible{outline:2px solid var(--blue);outline-offset:2px}
button.ghost{background:var(--btn-ghost);border-color:var(--line);color:var(--text)}
button.iconbtn{padding:10px;border-radius:12px}
button.iconbtn svg{width:18px;height:18px}
.ic{width:16px;height:16px;stroke:currentColor;fill:none;stroke-width:2;
  stroke-linecap:round;stroke-linejoin:round;flex:none}
.theme-sun{display:none}
:root[data-theme="light"] .theme-sun{display:block}
:root[data-theme="light"] .theme-moon{display:none}
.row{display:flex;align-items:center;gap:12px;flex-wrap:wrap}
.msg{color:var(--muted);font-size:13px;min-height:18px}
input,textarea{
  width:100%;border-radius:12px;border:1px solid var(--line);
  background:var(--input);color:var(--text);outline:none;
  transition:border-color .15s ease, background-color .2s ease;
}
input{padding:10px 12px;font:inherit}
textarea{
  min-height:200px;resize:vertical;padding:14px;
  font:13px/1.6 ui-monospace,SFMono-Regular,Consolas,monospace;
}
input:focus,textarea:focus{border-color:rgba(42,167,224,.55)}
input::placeholder{color:var(--muted);opacity:.8}
textarea::-webkit-scrollbar{width:10px}
textarea::-webkit-scrollbar-thumb{background:var(--line-strong);border-radius:8px}
textarea::-webkit-scrollbar-thumb:hover{background:var(--muted)}
textarea::-webkit-scrollbar-track{background:transparent}
.form{display:grid;grid-template-columns:repeat(auto-fit,minmax(170px,1fr));gap:12px}
.vtabs{display:flex;gap:8px;flex-wrap:wrap;margin-bottom:12px}
.vtabs button{padding:6px 12px;border-radius:999px;font-size:13px}
.vtabs button.on{border-color:rgba(42,167,224,.55);background:rgba(42,167,224,.16)}
.fbar{display:flex;gap:10px;flex-wrap:wrap;align-items:center;margin-bottom:10px}
.crumbs{display:flex;gap:6px;flex-wrap:wrap;font-size:13px;color:var(--muted)}
.crumbs a{cursor:pointer;color:var(--blue);text-decoration:none}
.crumbs a:hover{text-decoration:underline}
.crumbs a+a::before{content:"/";color:var(--muted);margin-right:6px}
.fup,.fmk{display:flex;gap:8px;align-items:center;flex-wrap:wrap}
.fup input[type=file]{width:auto;padding:6px;font-size:13px}
.fwrap{overflow-x:auto}
.ftable{width:100%;border-collapse:collapse;font-size:14px}
.ftable th{text-align:left;font-size:11px;letter-spacing:.4px;text-transform:uppercase;color:var(--muted);padding:6px 8px;border-bottom:1px solid var(--line)}
.ftable td{padding:8px;border-bottom:1px solid var(--line);vertical-align:middle}
.ftable tr:last-child td{border-bottom:0}
.ftable .num{text-align:right;font-variant-numeric:tabular-nums;color:var(--muted);white-space:nowrap}
.fname{display:flex;align-items:center;gap:8px;word-break:break-all}
.fname a{cursor:pointer;color:var(--text);text-decoration:none}
.fname a:hover{color:var(--blue)}
.detailwrap:empty{display:none}
.detail{display:flex;gap:16px;align-items:flex-start;margin-top:14px;padding:14px;border:1px solid var(--line);border-radius:14px;background:var(--btn-ghost)}
.dthumb{width:160px;height:160px;object-fit:cover;border-radius:10px;border:1px solid var(--line);background:var(--card)}
.dmeta{margin:0;display:grid;gap:8px;font-size:13px;min-width:0}
.dmeta div{display:grid;grid-template-columns:88px 1fr;gap:10px;align-items:baseline}
.dmeta dt{color:var(--muted);font-size:12px;letter-spacing:.3px;text-transform:uppercase}
.dmeta dd{margin:0;word-break:break-all}
.mono{font-family:ui-monospace,SFMono-Regular,Menlo,monospace;font-size:12px;color:var(--muted)}
@media(max-width:640px){.detail{flex-direction:column}.dthumb{width:100%;height:auto;max-height:240px}}
.acts{text-align:right;white-space:nowrap}
.acts .btn{display:inline-flex;align-items:center;padding:6px 10px;border-radius:9px;border:1px solid var(--line);color:var(--text);text-decoration:none;font-size:13px;background:var(--btn-ghost);margin-left:6px}
.acts .btn:hover{filter:brightness(1.1)}
.acts button{padding:6px 10px;font-size:13px;border-radius:9px;margin-left:6px}
.acts .danger{border-color:rgba(255,107,122,.45);background:transparent;color:var(--red)}
.finline{display:inline}
details.menu{display:inline-block;position:relative;margin-left:6px}
details.menu summary{list-style:none;cursor:pointer;padding:6px 10px;border-radius:9px;border:1px solid var(--line);font-size:13px;background:var(--btn-ghost)}
details.menu summary::-webkit-details-marker{display:none}
details.menu form{position:absolute;right:0;top:110%;z-index:5;display:flex;gap:6px;padding:10px;border-radius:12px;background:var(--card);border:1px solid var(--line-strong);box-shadow:var(--shadow)}
details.menu form input{width:240px}
.banner{grid-column:1/-1;border-radius:14px;padding:12px 16px;font-size:13px;line-height:1.6;display:none;
  color:var(--banner-text);background:var(--banner-bg);border:1px solid var(--banner-line)}
.banner.show{display:block}
.ok{color:var(--green)}
.err{color:var(--red)}
footer{margin-top:22px;color:var(--muted);font-size:12px;text-align:center}

/* ---- 视觉精修层：统一间距、层次与交互反馈 ---- */
:root{
  --radius:18px; --radius-sm:11px;
  --glass:rgba(11,18,32,.72);
  --head-bg:rgba(16,27,48,.94);
  --row-hover:rgba(255,255,255,.045);
  --ring:rgba(42,167,224,.45);
}
:root[data-theme="light"]{
  --glass:rgba(247,249,252,.82);
  --head-bg:#ffffff;
  --row-hover:rgba(16,32,52,.04);
}
body{font-size:15px;line-height:1.55;-webkit-font-smoothing:antialiased}
.wrap{max-width:1080px}
header{
  margin:0 0 20px;padding:0;
}
header h1{font-size:22px;font-weight:700;letter-spacing:-.2px}
header .sub{font-size:12.5px;letter-spacing:.2px}
.pill{backdrop-filter:blur(8px)}
.pill:not(.ok):not(.warn):not(.bad)::before{
  content:"";display:inline-block;width:7px;height:7px;border-radius:50%;
  background:var(--muted);margin-right:8px;vertical-align:1px;
}

main{gap:18px}
.card{
  position:relative;border-radius:var(--radius);padding:20px 20px 18px;
  overflow:hidden;
}
.card::before{
  content:"";position:absolute;inset:0 0 auto;height:1px;
  background:linear-gradient(90deg,transparent,var(--line-strong),transparent);
  opacity:.9;pointer-events:none;
}
.card:hover{transform:none;box-shadow:var(--shadow),0 0 0 1px var(--line-strong)}
.card h2{
  font-size:11.5px;letter-spacing:.9px;margin-bottom:16px;color:var(--muted);
}
.card h2 .ic{width:15px;height:15px;color:var(--blue);opacity:.9}

.vol{
  padding:13px 10px;margin:0 -10px;border-radius:12px;
  transition:background-color .15s ease;
}
.vol:hover{background:var(--row-hover)}
.vol .name{font-size:14.5px;letter-spacing:-.1px}
.vol .meta{font-size:12.5px}

.stats{gap:12px;margin-bottom:18px}
.stat{
  border-radius:var(--radius-sm);padding:12px 14px;
  transition:border-color .15s ease,background-color .15s ease;
}
.stat:hover{border-color:var(--line-strong);background:var(--card)}
.stat .k{font-size:10.5px;letter-spacing:.7px;text-transform:uppercase}
.stat strong{font-size:19px;font-weight:700;letter-spacing:-.3px}

button{
  min-height:38px;padding:9px 16px;border-radius:var(--radius-sm);
  box-shadow:inset 0 1px 0 rgba(255,255,255,.18),0 6px 16px rgba(42,167,224,.20);
  transition:transform .07s ease,filter .15s ease,box-shadow .15s ease,border-color .15s ease;
}
button.ghost,button.iconbtn{box-shadow:none}
button:hover{filter:brightness(1.06);box-shadow:inset 0 1px 0 rgba(255,255,255,.18),0 8px 22px rgba(42,167,224,.26)}
button.ghost:hover{box-shadow:none;border-color:var(--line-strong);background:var(--row-hover)}
button:disabled,button[hx-disabled-elt]{opacity:.55;cursor:progress;filter:saturate(.6)}
a.btn,a.ghost{transition:background-color .15s ease,border-color .15s ease}
input,textarea{border-radius:var(--radius-sm)}
input{padding:10px 13px;min-height:38px}
input:focus,textarea:focus{border-color:var(--ring);box-shadow:0 0 0 3px rgba(42,167,224,.14)}
input:hover,textarea:hover{border-color:var(--line-strong)}

.vtabs{gap:6px;margin-bottom:14px}
.vtabs button{min-height:32px;padding:5px 14px;font-size:12.5px;letter-spacing:.2px}
.vtabs button.on{color:#fff;background:linear-gradient(180deg,var(--blue),var(--blue-deep));border-color:transparent;box-shadow:0 4px 14px rgba(42,167,224,.28)}

.ftable thead th{
  position:sticky;top:0;z-index:1;padding:8px 10px;
  background:var(--head-bg);backdrop-filter:blur(6px);
}
.ftable th:nth-child(1){width:34px}
.ftable th:nth-child(2){width:auto}
.ftable th:nth-child(3){width:110px}
.ftable th:nth-child(4){width:168px}
.ftable th:nth-child(5){width:1%;white-space:nowrap}
.ftable td{padding:10px;border-bottom:1px solid var(--line)}
.ftable tbody tr{transition:background-color .12s ease}
.ftable tbody tr:hover{background:var(--row-hover)}
.ftable tbody tr:last-child td{border-bottom:0}
.ftable .empty{
  padding:28px 12px;text-align:center;color:var(--muted);
  border:1px dashed var(--line-strong);border-radius:12px;background:transparent;
}
.pager{display:flex;align-items:center;gap:12px;justify-content:center;padding:14px 0 2px}
.pager .pinfo{color:var(--muted);font-size:12.5px;font-variant-numeric:tabular-nums}
.pager button{min-height:32px;padding:5px 14px;font-size:12.5px}
.fname .ic{color:var(--muted);transition:color .15s ease}
.ftable tbody tr:hover .fname .ic{color:var(--blue)}
.fname a{font-weight:500}
.fwrap{margin:0 -4px;padding:0 4px}

.acts .btn,.acts button{min-height:30px;padding:5px 10px;font-size:12.5px}
.acts .btn{box-shadow:none}
.acts .btn:hover{background:var(--row-hover);border-color:var(--line-strong)}
details.menu summary{min-height:30px;display:inline-flex;align-items:center;transition:background-color .15s ease,border-color .15s ease}
details.menu summary:hover{background:var(--row-hover);border-color:var(--line-strong)}
details.menu form{padding:12px;gap:8px;border-radius:14px}

.crumbs{font-size:12.5px;letter-spacing:.2px}
.empty{color:var(--muted)}

/* 工具栏：上传与新建目录同一行，空间不足时才换行 */
.fbar{gap:12px;margin-bottom:12px}
.fup,.fmk{display:flex;gap:8px;align-items:center;flex-wrap:nowrap;min-width:0}
.fup{flex:1 1 340px}
.fmk{flex:0 1 auto}
.fup input[type=file]{flex:1 1 200px;min-width:0;padding:6px 8px;font-size:12.5px}
.fup button,.fmk button{flex:none}
.fmk input{width:180px;min-width:120px}
/* 原生文件选择框跟随主题，避免白底控件刺眼 */
input[type=file]{
  background:var(--btn-ghost);border-style:dashed;border-color:var(--line-strong);
  color:var(--muted);cursor:pointer;
}
input[type=file]::file-selector-button{
  margin-right:10px;padding:6px 10px;border:1px solid var(--line);
  border-radius:8px;background:var(--chip);color:var(--text);
  font:inherit;font-size:12.5px;cursor:pointer;
  transition:background-color .15s ease,border-color .15s ease;
}
input[type=file]::file-selector-button:hover{background:var(--row-hover);border-color:var(--line-strong)}

/* 上传进度条：宽度由 app.js 写 data-progress-bar 的 style.width */
.upwrap{display:inline-flex;align-items:center;width:120px;height:6px;border-radius:999px;background:var(--line);overflow:hidden;flex:none}
.upwrap[hidden]{display:none}
.upwrap i{display:block;height:100%;width:0;border-radius:999px;background:linear-gradient(90deg,var(--blue),var(--green));transition:width .18s ease}

/* 搜索/排序/批量工具栏 */
.fsearch{display:flex;gap:8px;align-items:center;flex:1 1 260px;min-width:0}
.fsearch input{flex:1 1 160px;min-width:0}
.fsorts .flabel{font-size:12px;color:var(--muted);text-transform:uppercase;letter-spacing:.4px}
.fsorts button{min-height:28px;padding:4px 11px;font-size:12.5px}
.fsorts button.on{color:var(--text);background:var(--row-hover);border-color:var(--line-strong)}
.fbatch{display:flex;gap:8px;align-items:center;flex-wrap:wrap;margin-bottom:12px;padding:9px 12px;border:1px solid var(--line);border-radius:12px;background:var(--row-hover)}
.fbatch select,.fbatch input{min-height:30px;padding:5px 9px;font-size:12.5px}
.fbatch .chk{display:inline-flex;align-items:center;gap:6px;font-size:12.5px;color:var(--muted)}
.fbatch input[type=checkbox]{width:15px;height:15px;min-height:0;padding:0}
.ftable .pick{width:34px;padding-right:0}
.ftable .pick input{width:15px;height:15px;min-height:0;padding:0}
.fnote{display:block;margin-top:2px;font-size:11.5px;color:var(--muted)}
.hidden{display:none!important}

/* 拖拽上传 */
.fwrap{position:relative}
.dropzone{
  position:absolute;inset:0;z-index:5;display:flex;align-items:center;justify-content:center;
  border:2px dashed var(--blue);border-radius:14px;background:rgba(42,167,224,.12);
  color:var(--text);font-size:14px;backdrop-filter:blur(2px);
}
.dropzone[hidden]{display:none}

/* 播放浮层 */
.playerwrap{position:fixed;inset:0;z-index:50;display:flex;align-items:center;justify-content:center;padding:24px;background:rgba(0,0,0,.62)}
.playerwrap[hidden]{display:none}
.playbox{width:min(1040px,100%);max-height:100%;display:flex;flex-direction:column;gap:12px;padding:16px;border:1px solid var(--line-strong);border-radius:16px;background:var(--card);box-shadow:0 24px 60px rgba(0,0,0,.45)}
.playhead{display:flex;align-items:center;gap:12px}
.playhead .name{font-weight:600;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.playhead .meta{color:var(--muted);font-size:12.5px;white-space:nowrap}
.playhead .btn,.playhead button{margin-left:auto}
.playhead button{margin-left:0}
.playbox video,.playbox audio{width:100%;border-radius:10px;background:#000}
.playimg{max-width:100%;max-height:70vh;object-fit:contain;border-radius:10px;align-self:center}
.pdframe{width:100%;height:70vh;border:0;border-radius:10px;background:#fff}

/* 统一滚动条（Firefox / WebKit） */
*{scrollbar-width:thin;scrollbar-color:var(--line-strong) transparent}
::-webkit-scrollbar{width:10px;height:10px}
::-webkit-scrollbar-thumb{background:var(--line-strong);border-radius:8px;border:2px solid transparent;background-clip:padding-box}
::-webkit-scrollbar-thumb:hover{background:var(--muted);background-clip:padding-box}
::-webkit-scrollbar-track{background:transparent}

@media(max-width:640px){
  .card{padding:16px;border-radius:16px}
  .stat strong{font-size:17px}
  .ftable td:nth-child(4),.ftable th:nth-child(4){display:none}
  .acts .btn,.acts button{min-height:34px}
  .fup,.fmk{flex-wrap:wrap;flex-basis:100%}
  .fmk input,.fup input[type=file]{flex:1 1 100%;width:100%}
  .vol .meta{width:100%;margin-left:0;text-align:left}
}
@media(max-width:400px){
  .head-actions{justify-content:flex-end;gap:8px}
}
.offline-acts{grid-column:1/-1;display:flex;flex-wrap:wrap;align-items:center;gap:8px;margin-top:10px}
.offline-acts .msg{min-width:0}
.oflist{display:flex;flex-direction:column;gap:6px;margin-top:10px}
.ofrow{display:flex;align-items:center;gap:10px;padding:8px 10px;border:1px solid var(--line);border-radius:10px;background:var(--chip);font-size:13px}
.ofrow .ofname{flex:1 1 auto;min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.ofrow .mono{color:var(--muted)}
@media(prefers-reduced-motion:reduce){
  *{animation:none!important;transition:none!important}
}
</style>
</head>
<body>
<div class="wrap">
  <header>
    <svg class="logo" viewBox="0 0 256 256" aria-hidden="true">
      <circle cx="128" cy="118" r="115" fill="#0f5a80"/>
      <circle cx="128" cy="118" r="108" fill="#229ED9"/>
      <path d="M44 134 L218 56 L143 202 Z" fill="#fff"/>
      <path d="M120 157 L218 56 L143 202 Z" fill="#cdebf9"/>
      <circle cx="195" cy="195" r="63" fill="rgba(0,0,0,.20)"/>
      <circle cx="193" cy="192" r="61" fill="#fff"/>
      <rect x="153" y="170" width="78" height="44" rx="10" fill="#4a5866"/>
      <rect x="153" y="170" width="78" height="15" rx="7" fill="#707f8d"/>
      <rect x="163" y="190" width="58" height="5" rx="2.5" fill="#212d38"/>
      <circle cx="221" cy="203" r="5" fill="#3ddc84"/>
    </svg>
    <div>
      <h1>{{.Labels.Title}}</h1>
      <p class="sub">{{.Labels.Subtitle}}</p>
    </div>
    <div class="head-actions">
      <button id="theme" class="ghost iconbtn" type="button" title="{{.Labels.ThemeTip}}" aria-label="{{.Labels.ThemeTip}}">
        <svg class="ic theme-sun" viewBox="0 0 24 24" aria-hidden="true">
          <circle cx="12" cy="12" r="4"/>
          <path d="M12 2v2M12 20v2M4.93 4.93l1.41 1.41M17.66 17.66l1.41 1.41M2 12h2M20 12h2M6.34 17.66l-1.41 1.41M19.07 4.93l-1.41 1.41"/>
        </svg>
        <svg class="ic theme-moon" viewBox="0 0 24 24" aria-hidden="true">
          <path d="M12 3a6 6 0 0 0 9 9 9 9 0 1 1-9-9Z"/>
        </svg>
      </button>
      <span id="pill" class="pill">…</span>
    </div>
  </header>

  <main>
    <div id="authbar" class="banner">{{.Labels.AuthLost}}</div>
    <div id="neterr" class="banner">{{.Labels.NetError}}</div>

    <section class="card">
      <h2>
        <svg class="ic" viewBox="0 0 24 24" aria-hidden="true">
          <line x1="22" x2="2" y1="12" y2="12"/>
          <path d="M5.45 5.11 2 12v6a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2v-6l-3.45-6.89A2 2 0 0 0 16.76 4H7.24a2 2 0 0 0-1.79 1.11Z"/>
          <line x1="6" x2="6.01" y1="16" y2="16"/>
          <line x1="10" x2="10.01" y1="16" y2="16"/>
        </svg>
        <span>{{.Labels.Volumes}}</span>
      </h2>
      <div id="volumes"><div class="empty">…</div></div>
    </section>

    <section class="card">
      <h2>
        <svg class="ic" viewBox="0 0 24 24" aria-hidden="true">
          <path d="M22 12h-4l-3 9L9 3l-3 9H2"/>
        </svg>
        <span>{{.Labels.Actions}}</span>
      </h2>
      <div id="stats" class="stats">
        <div class="stat"><span class="k">{{.Labels.StatVols}}</span><strong>0</strong></div>
        <div class="stat"><span class="k">{{.Labels.StatFiles}}</span><strong>0</strong></div>
        <div class="stat"><span class="k">{{.Labels.StatUsed}}</span><strong>—</strong></div>
      </div>
      <div class="row">
        {{if .CanSync}}<button hx-post="/frag/sync" hx-target="#syncmsg" hx-swap="innerHTML" hx-disabled-elt="this">
          <svg class="ic" viewBox="0 0 24 24" aria-hidden="true">
            <path d="M21 12a9 9 0 1 1-9-9c2.52 0 4.85 1.03 6.53 2.7L21 8"/>
            <path d="M21 3v5h-5"/>
          </svg>
          <span>{{.Labels.Sync}}</span>
        </button>{{end}}
        <button class="ghost iconbtn" type="button" hx-get="/frag/status" hx-swap="none" title="{{.Labels.RefreshTip}}" aria-label="{{.Labels.RefreshTip}}">
          <svg class="ic" viewBox="0 0 24 24" aria-hidden="true">
            <path d="M3 12a9 9 0 1 0 9-9 9.75 9.75 0 0 0-6.74 2.74L3 8"/>
            <path d="M3 3v5h5"/>
          </svg>
        </button>
      </div>
      <p class="msg" id="syncmsg"></p>
    </section>
{{if .Mount}}
    <section class="card" style="grid-column:1/-1">
      <h2>
        <svg class="ic" viewBox="0 0 24 24" aria-hidden="true">
          <rect width="20" height="8" x="2" y="2" rx="2" ry="2"/>
          <rect width="20" height="8" x="2" y="14" rx="2" ry="2"/>
          <line x1="6" x2="6.01" y1="6" y2="6"/>
          <line x1="6" x2="6.01" y1="18" y2="18"/>
        </svg>
        <span>{{.Labels.Mount}}</span>
      </h2>
      <div id="mountbox">{{template "mountbox" .Mount}}</div>
    </section>
{{end}}
{{if .Labels.CanCreate}}
    <section class="card" style="grid-column:1/-1">
      <h2>
        <svg class="ic" viewBox="0 0 24 24" aria-hidden="true">
          <path d="M20 20a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.9a2 2 0 0 1-1.69-.9L9.6 3.9A2 2 0 0 0 7.93 3H4a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2Z"/>
          <path d="M12 10v6M9 13h6"/>
        </svg>
        <span>{{.Labels.NewVolume}}</span>
      </h2>
      <form hx-post="/frag/volume" hx-target="#nv-msg" hx-swap="innerHTML">
        <div class="form">
          <input name="name" autocomplete="off" placeholder="{{.Labels.FieldName}}">
          <input name="title" autocomplete="off" placeholder="{{.Labels.FieldTitle}}">
          <input name="point" autocomplete="off" placeholder="{{.Labels.FieldPoint}}">
        </div>
        <div class="row" style="margin-top:12px">
          <button type="submit">
            <svg class="ic" viewBox="0 0 24 24" aria-hidden="true">
              <path d="M12 5v14M5 12h14"/>
            </svg>
            <span>{{.Labels.Create}}</span>
          </button>
          <span class="msg" id="nv-msg"></span>
        </div>
      </form>
    </section>
{{end}}
{{if .Browser}}
    <section class="card" id="files-card" style="grid-column:1/-1">
      <h2>
        <svg class="ic" viewBox="0 0 24 24" aria-hidden="true">
          <path d="M20 20a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.9a2 2 0 0 1-1.69-.9L9.6 3.9A2 2 0 0 0 7.93 3H4a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2Z"/>
        </svg>
        <span>{{.Labels.Files}}</span>
      </h2>
      <div id="browser">{{template "browser" .Browser}}</div>
      <div id="player" class="playerwrap" hidden></div>
    </section>
{{end}}
    <section class="card" style="grid-column:1/-1">
      <h2>
        <svg class="ic" viewBox="0 0 24 24" aria-hidden="true">
          <path d="M12 20h9"/>
          <path d="M16.5 3.5a2.12 2.12 0 0 1 3 3L7 19l-4 1 1-4Z"/>
        </svg>
        <span>{{.Labels.Config}}</span>
      </h2>
      <form hx-put="/frag/config" hx-target="#savemsg" hx-swap="innerHTML">
        <textarea id="cfg" name="content" spellcheck="false">{{.Config}}</textarea>
        <div class="row" style="margin-top:12px">
          <button type="submit">
            <svg class="ic" viewBox="0 0 24 24" aria-hidden="true">
              <path d="M19 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h11l5 5v11a2 2 0 0 1-2 2Z"/>
              <path d="M17 21v-8H7v8M7 3v5h8"/>
            </svg>
            <span>{{.Labels.Save}}</span>
          </button>
          <span class="msg" id="savemsg"></span>
        </div>
      </form>
    </section>
{{if .PWA.Enabled}}
    <section class="card" id="offline-card" style="grid-column:1/-1">
      <h2>
        <svg class="ic" viewBox="0 0 24 24" aria-hidden="true">
          <path d="M12 2v2M12 20v2M4.93 4.93l1.41 1.41M17.66 17.66l1.41 1.41M2 12h2M20 12h2M6.34 17.66l-1.41 1.41M19.07 4.93l-1.41 1.41"/>
          <circle cx="12" cy="12" r="4"/>
        </svg>
        <span>{{.Labels.FilesOffline}}</span>
        <button type="button" class="ghost" data-offline-refresh style="margin-left:auto">{{.Labels.RefreshTip}}</button>
      </h2>
      <p class="msg">{{.Labels.OfflineHint}}</p>
      <div id="offline-list" data-offline-list><div class="empty">…</div></div>
      <div id="offline-tpl" hidden data-offline-labels
           data-empty="{{.Labels.FilesEmpty}}"
           data-remove="{{.Labels.FilesOfflineRemove}}"
           data-open="{{.Labels.FilesDownload}}"></div>
    </section>
{{end}}
  </main>
  <footer>{{.Labels.Footer}}{{with .Version}} · {{.}}{{end}}</footer>
</div>
<div id="poller" hx-get="/frag/status" hx-trigger="load, status" hx-swap="none"></div>
<script src="/static/htmx.min.js"></script>
<script src="/static/app.js" defer></script>
</body>
</html>`
