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
 * 预期：
 *   - 只有带 data-upload 的表单受影响
 *   - 拿不到总长度时不动文案
 *   - 进度条按百分比伸展；请求结束或失败后收起
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

    const wrap = form?.querySelector<HTMLElement>("[data-progress-wrap]");
    const bar = form?.querySelector<HTMLElement>("[data-progress-bar]");
    if (wrap && bar) {
      wrap.hidden = false;
      bar.style.width = pct + "%";
    }
  });

  // 新一次提交前清空上一次的百分比（片段会被整体替换时其实用不上）
  document.body.addEventListener("htmx:beforeRequest", (ev) => {
    const form = (ev.target as Element | null)?.closest?.("[data-upload]");
    form?.querySelector<HTMLElement>("[data-progress]")?.replaceChildren();
    const wrap = form?.querySelector<HTMLElement>("[data-progress-wrap]");
    const bar = form?.querySelector<HTMLElement>("[data-progress-bar]");
    if (wrap && bar) {
      wrap.hidden = false;
      bar.style.width = "0%";
    }
  });

  // 结束或失败：收起进度条，文案由片段替换
  for (const name of ["htmx:afterRequest", "htmx:sendError"]) {
    document.body.addEventListener(name, (ev) => {
      const form = (ev.target as Element | null)?.closest?.("[data-upload]");
      const wrap = form?.querySelector<HTMLElement>("[data-progress-wrap]");
      if (wrap) {
        wrap.hidden = true;
      }
    });
  }
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

// ---------------------------------------------------------------------------
// 拖拽 / 目录上传 / 批量勾选 / 播放浮层
//
// 这四件事 htmx 的声明式属性做不到：拖拽要读 DataTransferItem 的目录树，
// 目录上传要保留 webkitRelativePath，批量要联动复选框，播放浮层要拦截点击。
// 全部用事件委托挂在 document 上，因为 #browser 会被片段整体替换。
// ---------------------------------------------------------------------------

declare const htmx: { ajax: (method: string, url: string, opts: Record<string, unknown>) => void };

/** 一条待上传文件：rel 是相对当前目录的路径，保留子目录结构。 */
interface UploadItem {
  file: File;
  rel: string;
}

/** FileSystemEntry 的最小形状（浏览器只在拖拽里给这个接口）。 */
interface EntryLike {
  isFile: boolean;
  isDirectory: boolean;
  name: string;
  file: (ok: (f: File) => void, fail?: (e: unknown) => void) => void;
  createReader: () => {
    readEntries: (ok: (list: EntryLike[]) => void, fail?: (e: unknown) => void) => void;
  };
}

/**
 * 单文件 XHR 上传，带进度回调。
 *
 * 输入：url；fd 表单；onPct 0..100 回调
 * 输出：成功 resolve，失败 reject
 * 预期：非 2xx 视为失败；网络错误也 reject
 */
function sendOne(url: string, fd: FormData, onPct: (pct: number) => void): Promise<void> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open("POST", url);
    xhr.upload.onprogress = (ev) => {
      if (ev.lengthComputable && ev.total > 0) {
        onPct(Math.round((ev.loaded * 100) / ev.total));
      }
    };
    xhr.onload = () => {
      if (xhr.status >= 200 && xhr.status < 300) {
        resolve();
      } else {
        reject(new Error("HTTP " + xhr.status));
      }
    };
    xhr.onerror = () => reject(new Error("network"));
    xhr.send(fd);
  });
}

/** 分片大小与走分片的阈值：小文件直传更省一次往返。 */
const CHUNK_SIZE = 4 * 1024 * 1024;
const CHUNK_THRESHOLD = 8 * 1024 * 1024;

/** 按毫秒等待，重试间隔用。 */
function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

/**
 * 分片上传单个文件。
 *
 * 输入：vol/dir 目标位置；item 文件与相对路径；onPct 0..100 回调
 * 输出：成功后 resolve
 * 预期：
 *   - 每片失败重试 3 次，仍失败就放弃会话
 *   - 服务端回 409 表示 offset 不一致，按它给的进度续传
 *   - commit 阶段把整份内容交给服务端入库，期间进度停在 100%
 */
async function uploadChunked(
  vol: string,
  dir: string,
  item: UploadItem,
  onPct: (pct: number) => void,
): Promise<void> {
  const init = await fetch("/api/upload/init", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      vol,
      dir,
      rel: item.rel,
      name: item.file.name,
      size: item.file.size,
    }),
  });
  if (!init.ok) {
    throw new Error("init " + init.status);
  }
  const { id } = (await init.json()) as { id: string };
  let off = 0;
  while (off < item.file.size) {
    const end = Math.min(off + CHUNK_SIZE, item.file.size);
    let tries = 0;
    for (;;) {
      try {
        const res = await fetch(`/api/upload/chunk?id=${id}&offset=${off}`, {
          method: "PUT",
          body: item.file.slice(off, end),
        });
        if (res.status === 409) {
          const cur = (await res.json()) as { offset: number };
          off = cur.offset;
          break;
        }
        if (!res.ok) {
          throw new Error("chunk " + res.status);
        }
        const cur = (await res.json()) as { offset: number };
        off = cur.offset;
        break;
      } catch (err) {
        tries++;
        if (tries >= 3) {
          await fetch(`/api/upload?id=${id}`, { method: "DELETE" });
          throw err;
        }
        await sleep(400 * tries);
      }
    }
    onPct(Math.round((off * 100) / item.file.size));
  }
  const done = await fetch(`/api/upload/commit?id=${id}`, { method: "POST" });
  if (!done.ok) {
    throw new Error("commit " + done.status);
  }
}

/**
 * 逐条上传并刷新列表。
 *
 * 输入：items 文件 + 相对路径
 * 输出：无（结束后用 htmx 重新拉当前目录）
 * 预期：
 *   - 串行发送，服务端本来就串行提交，避免白白排队
 *   - 进度按「已完成文件数 + 当前文件百分比」折算成整体百分比
 *   - 任一失败就停下并显示网络提示条
 */
async function uploadItems(items: UploadItem[]): Promise<void> {
  const form = document.querySelector<HTMLFormElement>("[data-upload]");
  if (!form || items.length === 0) {
    return;
  }
  const field = (name: string) => form.querySelector<HTMLInputElement>(`[name="${name}"]`)?.value ?? "";
  const vol = field("vol");
  const dir = field("dir");
  const out = form.querySelector<HTMLElement>("[data-progress]");
  const wrap = form.querySelector<HTMLElement>("[data-progress-wrap]");
  const bar = form.querySelector<HTMLElement>("[data-progress-bar]");
  if (wrap) {
    wrap.hidden = false;
  }
  let done = 0;
  for (const it of items) {
    const report = (pct: number) => {
      const overall = Math.round((done * 100 + pct) / items.length);
      if (out) {
        out.textContent = progressText(out, overall);
      }
      if (bar) {
        bar.style.width = overall + "%";
      }
    };
    try {
      if (it.file.size > CHUNK_THRESHOLD) {
        await uploadChunked(vol, dir, it, report);
      } else {
        const fd = new FormData();
        fd.set("vol", vol);
        fd.set("dir", dir);
        fd.set("rel", it.rel);
        fd.set("quiet", "1");
        fd.set("file", it.file);
        await sendOne("/frag/files/upload", fd, report);
      }
    } catch {
      document.getElementById("neterr")?.classList.add("show");
      break;
    }
    done++;
  }
  if (out) {
    out.textContent = progressText(out, 100);
  }
  if (bar) {
    bar.style.width = "100%";
  }
  htmx.ajax("GET", `/frag/files?vol=${encodeURIComponent(vol)}&dir=${encodeURIComponent(dir)}`, {
    target: "#browser",
    swap: "innerHTML",
  });
}

/**
 * 递归展开拖拽进来的目录。
 *
 * 输入：entry 文件或目录；prefix 当前相对路径前缀（以 / 结尾）
 * 输出：扁平的文件列表
 * 预期：readEntries 一次最多 100 条，必须循环读到空数组为止
 */
async function readEntry(entry: EntryLike, prefix: string): Promise<UploadItem[]> {
  if (entry.isFile) {
    const f = await new Promise<File>((ok, fail) => entry.file(ok, fail));
    return [{ file: f, rel: prefix + f.name }];
  }
  if (!entry.isDirectory) {
    return [];
  }
  const reader = entry.createReader();
  const out: UploadItem[] = [];
  for (;;) {
    const batch = await new Promise<EntryLike[]>((ok, fail) => reader.readEntries(ok, fail));
    if (batch.length === 0) {
      break;
    }
    for (const child of batch) {
      out.push(...(await readEntry(child, prefix + entry.name + "/")));
    }
  }
  return out;
}

/** 拖拽上传：进入文件区域时显示提示层，松手后递归收集并上传。 */
function initDropzone(): void {
  let depth = 0;
  const zoneOf = (ev: DragEvent) =>
    (ev.target as Element | null)?.closest?.("[data-drop]") ?? null;
  const hint = (zone: Element | null, show: boolean) => {
    const el = zone?.querySelector<HTMLElement>("[data-dropzone]");
    if (el) {
      el.hidden = !show;
    }
  };

  document.addEventListener("dragenter", (ev) => {
    const zone = zoneOf(ev);
    if (!zone) {
      return;
    }
    ev.preventDefault();
    depth++;
    hint(zone, true);
  });
  document.addEventListener("dragover", (ev) => {
    if (zoneOf(ev)) {
      ev.preventDefault();
    }
  });
  document.addEventListener("dragleave", (ev) => {
    const zone = zoneOf(ev);
    if (!zone) {
      return;
    }
    depth = Math.max(0, depth - 1);
    if (depth === 0) {
      hint(zone, false);
    }
  });
  document.addEventListener("drop", (ev) => {
    const zone = zoneOf(ev);
    if (!zone) {
      return;
    }
    ev.preventDefault();
    depth = 0;
    hint(zone, false);
    const dt = ev.dataTransfer;
    if (!dt) {
      return;
    }
    void (async () => {
      const items: UploadItem[] = [];
      for (const it of Array.from(dt.items)) {
        if (it.kind !== "file") {
          continue;
        }
        const entry = (
          it as DataTransferItem & { webkitGetAsEntry?: () => EntryLike | null }
        ).webkitGetAsEntry?.() as unknown as EntryLike | null;
        if (entry) {
          items.push(...(await readEntry(entry, "")));
        } else {
          const f = it.getAsFile();
          if (f) {
            items.push({ file: f, rel: f.name });
          }
        }
      }
      await uploadItems(items);
    })();
  });
}

/** 目录上传：按钮触发隐藏的 webkitdirectory 输入，选完按相对路径上传。 */
function initDirectoryUpload(): void {
  document.addEventListener("click", (ev) => {
    const btn = (ev.target as Element | null)?.closest?.("[data-dirbtn]");
    if (!btn) {
      return;
    }
    btn.closest("[data-upload]")?.querySelector<HTMLInputElement>("[data-dirpick]")?.click();
  });
  document.addEventListener("change", (ev) => {
    const input = ev.target;
    if (!(input instanceof HTMLInputElement) || !input.hasAttribute("data-dirpick")) {
      return;
    }
    const files = Array.from(input.files ?? []);
    if (files.length === 0) {
      return;
    }
    const rel = (f: File) =>
      (f as File & { webkitRelativePath?: string }).webkitRelativePath || f.name;
    void uploadItems(files.map((f) => ({ file: f, rel: rel(f) })));
    input.value = "";
  });
}

/** 批量勾选：表头复选框联动所有行内复选框。 */
function initSelectAll(): void {
  document.addEventListener("change", (ev) => {
    const box = ev.target;
    if (!(box instanceof HTMLInputElement) || !box.hasAttribute("data-checkall")) {
      return;
    }
    document.querySelectorAll<HTMLInputElement>('input[name="paths"]').forEach((el) => {
      el.checked = box.checked;
    });
  });
}

/** 播放浮层：htmx 把片段塞进 #player 后解除 hidden，关闭键或点背景清空。 */
function initPlayer(): void {
  const close = () => {
    const p = document.getElementById("player");
    if (!p) {
      return;
    }
    p.innerHTML = "";
    p.hidden = true;
  };
  document.addEventListener("click", (ev) => {
    const t = ev.target as Element | null;
    if (t?.closest?.("[data-close-player]")) {
      ev.preventDefault();
      close();
      return;
    }
    const p = document.getElementById("player");
    if (p && !p.hidden && t === p) {
      close();
    }
  });
  document.addEventListener("keydown", (ev) => {
    if (ev.key === "Escape") {
      close();
    }
  });
  document.body.addEventListener("htmx:afterSwap", (ev) => {
    const el = (ev as CustomEvent<{ target?: Element }>).detail?.target;
    if (el && el.id === "player") {
      (el as HTMLElement).hidden = false;
    }
  });
}

// ---------------------------------------------------------------------------
// PWA：注册 service worker，管理「离线保存」的文件。
//
// 所有文案由服务端渲染进 data-* 属性，这里只做状态切换，不写死任何字符串。
// 与 SW 的通信走 postMessage，逻辑键用 vol\u0000path 比对，避开 URL 编码差异。
// ---------------------------------------------------------------------------

/** SW 消息回传的离线文件条目。 */
interface OfflineItem {
  key: string;
  url: string;
  vol: string;
  path: string;
  name: string;
}

/** 离线相关文案，从 #offline-tpl 的 data-* 读取。 */
interface OfflineLabels {
  empty: string;
  remove: string;
  open: string;
}

/** 面板里显示的离线状态文案（i18n 注入到详情面板的 data-*）。 */
interface OfflineTexts {
  saved: string;
  removed: string;
  fail: string;
  large: string;
}

function offlineLabels(): OfflineLabels {
  const tpl = document.getElementById("offline-tpl");
  return {
    empty: tpl?.dataset.offlineEmpty ?? "",
    remove: tpl?.dataset.offlineRemove ?? "",
    open: tpl?.dataset.offlineOpen ?? "",
  };
}

/** 详情面板的离线文案：挂在按钮 data-* 上，由服务端模板写入。 */
function offlineTexts(group: HTMLElement): OfflineTexts {
  return {
    saved: group.dataset.offlineSaved ?? "",
    removed: group.dataset.offlineRemoved ?? "",
    fail: group.dataset.offlineFail ?? "",
    large: group.dataset.offlineLarge ?? "",
  };
}

/** 构造 vol\u0000path 逻辑键，与 SW 的 logicalKey 保持一致。 */
function logicalKeyOf(url: string): string {
  try {
    const u = new URL(url, location.origin);
    return (u.searchParams.get("vol") || "") + "\u0000" + (u.searchParams.get("path") || "");
  } catch {
    return "";
  }
}

/** 当前受控的 ServiceWorker（可能还没激活，返回 registration.active）。 */
async function swReady(): Promise<ServiceWorker | null> {
  if (!("serviceWorker" in navigator)) {
    return null;
  }
  try {
    const reg = await navigator.serviceWorker.ready;
    return reg.active;
  } catch {
    return null;
  }
}

/** 给 SW 发消息并等待同类型回执；超时按失败处理。 */
function swAsk(
  worker: ServiceWorker,
  message: Record<string, unknown>,
): Promise<Record<string, any>> {
  return new Promise((resolve, reject) => {
    const ch = new MessageChannel();
    const timer = setTimeout(() => reject(new Error("timeout")), 30000);
    ch.port1.onmessage = (ev) => {
      clearTimeout(timer);
      resolve((ev.data || {}) as Record<string, any>);
    };
    try {
      worker.postMessage(message, [ch.port2]);
    } catch (err) {
      clearTimeout(timer);
      reject(err);
    }
  });
}

let offlineSet: Set<string> = new Set();

/** 依据离线集合刷新所有详情面板的按钮显隐。 */
function refreshOfflineButtons(): void {
  document.querySelectorAll<HTMLElement>("[data-offline-group]").forEach((group) => {
    const key = logicalKeyOf(group.dataset.offlineUrl ?? "");
    const saved = offlineSet.has(key);
    const save = group.querySelector<HTMLElement>("[data-offline-save]");
    const remove = group.querySelector<HTMLElement>("[data-offline-remove]");
    if (save) {
      save.hidden = saved;
    }
    if (remove) {
      remove.hidden = !saved;
    }
  });
}

/** 渲染离线文件列表卡片。 */
function renderOfflineList(items: OfflineItem[]): void {
  const box = document.getElementById("offline-list");
  if (!box) {
    return;
  }
  const t = offlineLabels();
  offlineSet = new Set(items.map((it) => it.key));
  if (items.length === 0) {
    const empty = document.createElement("div");
    empty.className = "empty";
    empty.textContent = t.empty;
    box.replaceChildren(empty);
  } else {
    const wrap = document.createElement("div");
    wrap.className = "oflist";
    for (const it of items) {
      const row = document.createElement("div");
      row.className = "ofrow";
      const name = document.createElement("span");
      name.className = "ofname";
      name.textContent = it.name;
      const vol = document.createElement("span");
      vol.className = "mono";
      vol.textContent = it.vol + ":" + it.path;
      const open = document.createElement("a");
      open.className = "btn";
      open.href = it.url;
      open.setAttribute("download", "");
      open.textContent = t.open;
      const rm = document.createElement("button");
      rm.type = "button";
      rm.className = "ghost";
      rm.dataset.offlineListRemove = it.url;
      rm.textContent = t.remove;
      row.append(name, vol, open, rm);
      wrap.append(row);
    }
    box.replaceChildren(wrap);
  }
  refreshOfflineButtons();
}

/** 向 SW 请求当前离线文件列表。 */
async function loadOfflineList(): Promise<void> {
  const worker = await swReady();
  if (!worker) {
    return;
  }
  try {
    const res = await swAsk(worker, { type: "list" });
    renderOfflineList((res.items || []) as OfflineItem[]);
  } catch {
    /* SW 未就绪：列表保持占位，下次操作再刷新 */
  }
}

/** 把详情面板里的动作结果写进提示区。 */
function setOfflineMsg(group: HTMLElement, text: string, kind: "ok" | "err"): void {
  const out = group.querySelector<HTMLElement>("[data-offline-msg]");
  if (!out) {
    return;
  }
  out.textContent = text;
  out.className = "msg " + kind;
}

/**
 * 注册 service worker 并挂好离线交互。
 *
 * 输入：无
 * 输出：无
 * 预期：
 *   - 页面无 #offline-card（PWA 关闭）时不注册、不绑定
 *   - 所有用户可见文案取自 DOM data-*，本文件无硬编码字符串
 *   - 保存/删除成功后刷新列表与详情按钮，失败就地提示
 */
function initOffline(): void {
  const enabled = document.getElementById("offline-card");
  if (!enabled || !("serviceWorker" in navigator)) {
    return;
  }
  navigator.serviceWorker.register("/sw.js").catch(() => {
    /* 不支持或注册失败：离线功能不可用，面板其余部分照常 */
  });

  // 详情面板按钮：事件委托，#browser 会被片段整体替换
  document.addEventListener("click", (ev) => {
    const btn = ev.target as Element | null;
    const save = btn?.closest<HTMLElement>("[data-offline-save]");
    const remove = btn?.closest<HTMLElement>("[data-offline-remove]");
    if (save) {
      void offlineAction(save, "cache");
    } else if (remove) {
      void offlineAction(remove, "uncache");
    }
  });

  // 列表里的删除按钮
  document.addEventListener("click", (ev) => {
    const rm = (ev.target as Element | null)?.closest<HTMLElement>("[data-offline-list-remove]");
    if (rm?.dataset.offlineListRemove) {
      void offlineListRemove(rm.dataset.offlineListRemove);
    }
  });

  // 卡片刷新按钮 + 首屏加载
  document.querySelector("[data-offline-refresh]")?.addEventListener("click", () => {
    void loadOfflineList();
  });
  void loadOfflineList();
}

/** 详情面板的保存/删除动作。 */
async function offlineAction(btn: HTMLElement, type: "cache" | "uncache"): Promise<void> {
  const group = btn.closest<HTMLElement>("[data-offline-group]");
  if (!group) {
    return;
  }
  const url = group.dataset.offlineUrl ?? "";
  const max = Number(group.dataset.offlineMax ?? "0");
  const t = offlineTexts(group);
  const worker = await swReady();
  if (!worker) {
    setOfflineMsg(group, t.fail, "err");
    return;
  }
  try {
    const msg: Record<string, unknown> = { type, url };
    if (type === "cache") {
      msg.max = max;
    }
    const res = await swAsk(worker, msg);
    if (!res.ok) {
      const text = res.error === "too-large" ? t.large : t.fail;
      setOfflineMsg(group, text, "err");
      return;
    }
    setOfflineMsg(group, type === "cache" ? t.saved : t.removed, "ok");
    await loadOfflineList();
  } catch {
    setOfflineMsg(group, t.fail, "err");
  }
}

/** 列表里的删除动作：删完刷新列表。 */
async function offlineListRemove(url: string): Promise<void> {
  const worker = await swReady();
  if (!worker) {
    return;
  }
  try {
    await swAsk(worker, { type: "uncache", url });
    await loadOfflineList();
  } catch {
    /* 刷新失败保持现状 */
  }
}

function main(): void {
  initTheme();
  initUploadProgress();
  initMenus();
  initErrorBridge();
  initEvents();
  initDropzone();
  initDirectoryUpload();
  initSelectAll();
  initPlayer();
  initOffline();
}

if (document.readyState === "loading") {
  document.addEventListener("DOMContentLoaded", main);
} else {
  main();
}
