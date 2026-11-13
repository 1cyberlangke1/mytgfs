// 面板前端：只做 htmx 管不了的三件事——主题偏好、上传进度、行内菜单收起。
//
// 约定：
//   - 所有面向用户的文案都由服务端渲染在 DOM 里（title / placeholder / data-*），
//     这个文件不出现任何硬编码字符串。
//   - 业务请求全部走 htmx 属性，这里不写 fetch。
//   - htmx 由 /static/htmx.min.js 提供，页面加载顺序由 <script defer> 保证。

type Theme = "dark" | "light";

interface ProgressDetail {
  loaded: number;
  total: number;
  lengthComputable: boolean;
}

const THEME_KEY = "mytgfs-theme";

/** 读 localStorage；无痕模式下会抛异常，这里吞掉。 */
function safeGet(key: string): string | null {
  try {
    return localStorage.getItem(key);
  } catch {
    return null;
  }
}

function safeSet(key: string, value: string): void {
  try {
    localStorage.setItem(key, value);
  } catch {
    /* 无痕模式：忽略，主题只在本次会话生效 */
  }
}

function isTheme(value: string | null): value is Theme {
  return value === "dark" || value === "light";
}

/** 系统偏好；没有 matchMedia 时按深色处理。 */
function systemTheme(): Theme {
  const mq = window.matchMedia?.("(prefers-color-scheme: light)");
  return mq?.matches ? "light" : "dark";
}

/** 当前生效的主题（首屏内联脚本已经写过一次）。 */
function currentTheme(): Theme {
  return document.documentElement.dataset.theme === "light" ? "light" : "dark";
}

function applyTheme(theme: Theme): void {
  document.documentElement.dataset.theme = theme;
  const btn = document.getElementById("theme");
  if (btn) {
    btn.setAttribute("aria-pressed", String(theme === "light"));
  }
}

/**
 * 主题：手动选择优先，没选过才跟随系统。
 *
 * 输入：无（读 DOM 与 localStorage）
 * 输出：无
 * 预期：点击按钮立即切换并记住；系统主题变化只在没手动选过时生效
 */
function initTheme(): void {
  applyTheme(currentTheme());

  document.getElementById("theme")?.addEventListener("click", () => {
    const next: Theme = currentTheme() === "light" ? "dark" : "light";
    safeSet(THEME_KEY, next);
    applyTheme(next);
  });

  const mq = window.matchMedia?.("(prefers-color-scheme: light)");
  mq?.addEventListener("change", () => {
    if (isTheme(safeGet(THEME_KEY))) {
      return;
    }
    applyTheme(systemTheme());
  });
}

/**
 * 把进度模板里的 {pct} 换成实际百分比。
 *
 * 输入：el 带 data-progress-label 的元素；pct 0..100
 * 输出：展示文案
 * 预期：模板缺 {pct} 时原样返回，不抛异常
 */
function progressText(el: HTMLElement, pct: number): string {
  const tpl = el.dataset.progressLabel ?? "";
  return tpl.replace("{pct}", String(pct));
}

/**
 * 上传进度：htmx 在 XHR 上派发 progress，这里写进同一表单里的指示器。
 *
 * 输入：无
 * 输出：无
 * 预期：只有带 data-upload 的表单受影响；拿不到总长度时不动文案
 */
function initUploadProgress(): void {
  document.body.addEventListener("htmx:xhr:progress", (ev) => {
    const detail = (ev as CustomEvent<ProgressDetail>).detail;
    if (!detail?.lengthComputable || detail.total <= 0) {
      return;
    }
    const form = (ev.target as Element | null)?.closest?.("[data-upload]");
    const out = form?.querySelector<HTMLElement>("[data-progress]");
    if (!out) {
      return;
    }
    const pct = Math.min(100, Math.round((detail.loaded * 100) / detail.total));
    out.textContent = progressText(out, pct);
  });

  // 新一次提交前清空上一次的百分比（片段会被整体替换时其实用不上）
  document.body.addEventListener("htmx:beforeRequest", (ev) => {
    const form = (ev.target as Element | null)?.closest?.("[data-upload]");
    form?.querySelector<HTMLElement>("[data-progress]")?.replaceChildren();
  });
}

/**
 * 行内菜单：点空白处收起所有已展开的 details。
 *
 * 输入：无
 * 输出：无
 * 预期：点菜单内部不收起，避免和表单交互打架
 */
function initMenus(): void {
  document.addEventListener("click", (ev) => {
    const target = ev.target as Element | null;
    if (target?.closest("details.menu")) {
      return;
    }
    document.querySelectorAll<HTMLDetailsElement>("details.menu[open]").forEach((el) => {
      el.open = false;
    });
  });
}

/**
 * 网络错误提示条：htmx 只在连接失败时派发 sendError。
 *
 * 输入：无
 * 输出：无
 * 预期：出现一次即显示，下一次请求成功后被隐藏
 */
function initErrorBridge(): void {
  document.body.addEventListener("htmx:sendError", () => {
    document.getElementById("neterr")?.classList.add("show");
  });
  document.body.addEventListener("htmx:afterRequest", () => {
    document.getElementById("neterr")?.classList.remove("show");
  });
}

/**
 * 服务端事件：配置与挂载变更由后端推送，不再定时轮询。
 *
 * 输入：无（订阅 /events）
 * 输出：无
 * 预期：
 *   - mount 事件只刷新 #mountbox（不存在则忽略）
 *   - config 事件只更新配置正文，且不覆盖用户正在编辑的内容
 *   - EventSource 断线由浏览器自动重连
 */
function initEvents(): void {
  if (typeof EventSource === "undefined") {
    return;
  }
  const es = new EventSource("/events");

  es.addEventListener("mount", () => {
    void refreshMount();
  });
  es.addEventListener("config", () => {
    void refreshConfig();
  });
}

/** 重新拉取挂载卡片。 */
async function refreshMount(): Promise<void> {
  const box = document.getElementById("mountbox");
  if (!box) {
    return;
  }
  try {
    const res = await fetch("/frag/mount", { headers: { "HX-Request": "true" } });
    if (res.ok) {
      box.innerHTML = await res.text();
    }
  } catch {
    /* 断网时忽略，下一次事件会重试 */
  }
}

/** 重新拉取配置正文；正在编辑时不动，避免打断输入。 */
async function refreshConfig(): Promise<void> {
  const ta = document.getElementById("cfg");
  if (!(ta instanceof HTMLTextAreaElement) || ta === document.activeElement) {
    return;
  }
  try {
    const res = await fetch("/api/config");
    if (res.ok) {
      ta.value = await res.text();
    }
  } catch {
    /* 同上 */
  }
}

function main(): void {
  initTheme();
  initUploadProgress();
  initMenus();
  initErrorBridge();
  initEvents();
}

if (document.readyState === "loading") {
  document.addEventListener("DOMContentLoaded", main);
} else {
  main();
}
