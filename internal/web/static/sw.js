// mytgfs 的 service worker：预缓存外壳，按需缓存整份文件。
//
// 职责边界：
//   - 外壳（页面/脚本/图标/manifest）装一次，之后离线也能打开面板
//   - 文件不进预缓存：只有用户点「离线保存」才写进 FILE_CACHE
//   - 不拦截 Range 请求；播放器在线时永远走网络，避免整份回放
//
// 版本号由服务端在 /sw.js 里替换：发布新版本即换缓存名，旧缓存自动清掉。

const VERSION = "__VERSION__";
const SHELL_CACHE = "mytgfs-shell-" + VERSION;
const FILE_CACHE = "mytgfs-files-" + VERSION;
const SHELL = [
  "/",
  "/static/htmx.min.js",
  "/static/app.js",
  "/manifest.webmanifest",
  "/static/icon-192.png",
  "/static/icon-512.png",
];

/**
 * 把下载地址归一成缓存键 URL：只保留 vol/path，用 encodeURIComponent 重建。
 *
 * 输入：URL 对象
 * 输出：缓存键字符串；不是文件下载地址时返回 null
 * 预期：
 *   - 与页面传来的 URL 编码形式无关（Go 用 + 表示空格，JS 用 %20），
 *     这里统一按解码后的值重建，保证 saveFile 与 serveFile 命中同一份
 *   - inline / 随机参数不影响命中
 */
function fileURLKey(url) {
  if (!url.pathname.startsWith("/files/download")) {
    return null;
  }
  const vol = url.searchParams.get("vol");
  const path = url.searchParams.get("path");
  if (!vol || path === null || path === "") {
    return null;
  }
  return "/files/download?vol=" + encodeURIComponent(vol) + "&path=" + encodeURIComponent(path);
}

/**
 * 逻辑键：给页面与 SW 通信比对用，与缓存键的编码差异无关。
 *
 * 输入：URL 对象
 * 输出：vol + NUL + path（searchParams.get 已解码）
 */
function logicalKey(url) {
  return (url.searchParams.get("vol") || "") + "\u0000" + (url.searchParams.get("path") || "");
}

/** 从卷内路径取文件名。 */
function baseName(path) {
  const parts = path.split("/");
  return parts[parts.length - 1] || path;
}

/**
 * 回结果给触发页。
 *
 * 输入：port 页面随消息 transfer 过来的 MessagePort；message 回执
 * 输出：无
 * 预期：用端口一对一回复，不会串到别的请求；页面已关闭时忽略异常
 */
function reply(port, message) {
  try {
    port?.postMessage(message);
  } catch (err) {
    /* 页面已关闭 */
  }
}

self.addEventListener("install", (event) => {
  event.waitUntil(
    (async () => {
      const cache = await caches.open(SHELL_CACHE);
      // 单个资源失败不该让整个安装失败：缺哪个下次访问时再补
      await Promise.all(
        SHELL.map((url) =>
          cache.add(new Request(url, { cache: "reload" })).catch(() => undefined),
        ),
      );
      await self.skipWaiting();
    })(),
  );
});

self.addEventListener("activate", (event) => {
  event.waitUntil(
    (async () => {
      const keep = [SHELL_CACHE, FILE_CACHE];
      for (const name of await caches.keys()) {
        if (!keep.includes(name)) {
          await caches.delete(name);
        }
      }
      await self.clients.claim();
    })(),
  );
});

self.addEventListener("fetch", (event) => {
  const req = event.request;
  if (req.method !== "GET") {
    return;
  }
  const url = new URL(req.url);
  if (url.origin !== self.location.origin) {
    return;
  }

  // Range 一律不拦截：播放器要切片，缓存里只有整份内容
  if (req.headers.has("range")) {
    return;
  }

  if (fileURLKey(url)) {
    event.respondWith(serveFile(req, fileURLKey(url)));
    return;
  }

  // 静态资源与 manifest 都缓存优先：断网时浏览器重新取 manifest 也不会报错
  if (url.pathname.startsWith("/static/") || url.pathname === "/manifest.webmanifest") {
    event.respondWith(cacheFirst(req, SHELL_CACHE));
    return;
  }

  if (req.mode === "navigate") {
    event.respondWith(serveShell(req));
  }
});

/**
 * 文件请求：缓存优先，未命中再走网络。
 *
 * 输入：req 原始请求；key 归一后的缓存键
 * 输出：Response
 * 预期：显式保存过的文件离线可下载；未保存的在线正常走网络
 */
async function serveFile(req, key) {
  const cache = await caches.open(FILE_CACHE);
  const hit = await cache.match(key);
  if (hit) {
    return hit;
  }
  return fetch(req);
}

/**
 * 静态资源：缓存优先，未命中再取网络并补进缓存。
 */
async function cacheFirst(req, name) {
  const cache = await caches.open(name);
  const hit = await cache.match(req, { ignoreSearch: false });
  if (hit) {
    return hit;
  }
  const res = await fetch(req);
  if (res.ok) {
    await cache.put(req, res.clone());
  }
  return res;
}

/**
 * 页面导航：网络优先，断网时用预缓存的首屏。
 */
async function serveShell(req) {
  try {
    const res = await fetch(req);
    if (res.ok && res.type === "basic") {
      const cache = await caches.open(SHELL_CACHE);
      await cache.put("/", res.clone());
    }
    return res;
  } catch (err) {
    const cache = await caches.open(SHELL_CACHE);
    const hit = await cache.match("/");
    if (hit) {
      return hit;
    }
    throw err;
  }
}

self.addEventListener("message", (event) => {
  const data = event.data || {};
  // 页面用 MessageChannel 传端口，一对一回执，避免多请求串线
  const port = event.ports && event.ports[0];
  if (data.type === "cache") {
    event.waitUntil(saveFile(data.url, data.max || 0, port));
  } else if (data.type === "uncache") {
    event.waitUntil(dropFile(data.url, port));
  } else if (data.type === "list") {
    event.waitUntil(listFiles(port));
  }
});

/**
 * 下载并缓存整份文件。
 *
 * 输入：url 下载地址；max 字节上限（0 不限制）；source 触发页
 * 输出：无（结果通过 postMessage 回传 {type:"cache-result"}）
 * 预期：
 *   - 超过 max 直接拒绝，不浪费流量
 *   - 非 2xx 视为失败
 *   - 配额不足时如实报错，不静默成功
 */
async function saveFile(url, max, port) {
  const abs = new URL(url, self.location.origin);
  const key = fileURLKey(abs);
  if (!key) {
    reply(port, { type: "cache-result", url, ok: false, error: "bad-url" });
    return;
  }
  const meta = { key: logicalKey(abs), vol: abs.searchParams.get("vol") || "", path: abs.searchParams.get("path") || "" };
  try {
    const res = await fetch(abs.href);
    if (!res.ok) {
      reply(port, { type: "cache-result", url, ok: false, error: "http-" + res.status, ...meta });
      return;
    }
    const len = Number(res.headers.get("content-length") || 0);
    if (max > 0 && len > max) {
      await res.body?.cancel();
      reply(port, { type: "cache-result", url, ok: false, error: "too-large", ...meta });
      return;
    }
    const cache = await caches.open(FILE_CACHE);
    await cache.put(key, res.clone());
    reply(port, { type: "cache-result", url, ok: true, size: len, ...meta });
  } catch (err) {
    reply(port, { type: "cache-result", url, ok: false, error: String(err && err.name), ...meta });
  }
}

/** 删除一份离线文件。 */
async function dropFile(url, port) {
  const abs = new URL(url, self.location.origin);
  const key = fileURLKey(abs);
  const cache = await caches.open(FILE_CACHE);
  let ok = false;
  if (key) {
    ok = await cache.delete(key);
  }
  reply(port, {
    type: "uncache-result", url, ok,
    key: logicalKey(abs),
    vol: abs.searchParams.get("vol") || "",
    path: abs.searchParams.get("path") || "",
  });
}

/** 列出已离线保存的文件。 */
async function listFiles(port) {
  const cache = await caches.open(FILE_CACHE);
  const items = [];
  for (const req of await cache.keys()) {
    const u = new URL(req.url, self.location.origin);
    const path = u.searchParams.get("path") || "";
    items.push({
      key: logicalKey(u),
      url: req.url,
      vol: u.searchParams.get("vol") || "",
      path,
      name: baseName(path),
    });
  }
  reply(port, { type: "list-result", items });
}
