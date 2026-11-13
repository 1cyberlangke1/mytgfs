// 面板与本地 Go 服务之间的 JSON API 客户端。
//
// 所有请求同源（桌面壳走 webview，网页版走 127.0.0.1），
// 因此没有鉴权头；错误统一抛 ApiError，由调用方转成 toast。

export interface ApiFile {
  id: string;
  volumeId: string;
  parentId: string | null;
  name: string;
  isDir: boolean;
  size: number;
  updatedAt: string;
  category: string;
  path: string;
  thumbnailUrl?: string;
  previewUrl?: string;
}

export interface ApiVolume {
  id: string;
  name: string;
  entries: number;
  used: number;
  cover?: boolean;
}

export interface ApiTransfer {
  path: string;
  done: number;
  total: number;
}

export interface ApiMount {
  available: boolean;
  enabled: boolean;
  addr: string;
  volume: string;
}

export interface ApiQueueItem {
  id: number;
  name: string;
  state: string;
  attempt: number;
  error?: string;
}

export interface ApiState {
  volumes: ApiVolume[];
  caps?: { createVolume?: boolean; deleteVolume?: boolean };
  status?: {
    state?: string;
    state_code?: string;
    transfers?: ApiTransfer[];
    queue?: ApiQueueItem[];
  };
  mount?: ApiMount;
}

export interface ApiSettingDef {
  key: string;
  kind: 'string' | 'int' | 'bool';
}

export interface ApiSettings {
  schema: ApiSettingDef[];
  values: Record<string, string>;
  rawConfigText: string;
}

export class ApiError extends Error {
  constructor(
    message: string,
    readonly status: number
  ) {
    super(message);
    this.name = 'ApiError';
  }
}

// asMessage 兼容两种错误体：{error:{message}} 与 {error:"..."}。
function asMessage(payload: unknown): string {
  if (!payload || typeof payload !== 'object') return '';
  const err = (payload as { error?: unknown }).error;
  if (!err) return '';
  if (typeof err === 'string') return err;
  if (typeof err === 'object' && err && 'message' in err) {
    return String((err as { message: unknown }).message);
  }
  return '';
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, init);
  const text = await res.text();
  let payload: unknown = null;
  if (text) {
    try {
      payload = JSON.parse(text);
    } catch {
      payload = null;
    }
  }
  if (!res.ok) {
    throw new ApiError(asMessage(payload) || `${res.status} ${res.statusText}`, res.status);
  }
  return payload as T;
}

// getState 读取卷列表、连接状态与传输进度。
export function getState(): Promise<ApiState> {
  return request<ApiState>('/api/state');
}

// listFiles 拉取整卷条目（all=1）或单目录。
export async function listFiles(volume: string, all = false, dir = '/'): Promise<ApiFile[]> {
  const q = new URLSearchParams({ vol: volume });
  if (all) q.set('all', '1');
  else q.set('dir', dir);
  const res = await request<{ files: ApiFile[] }>(`/api/files?${q.toString()}`);
  return res.files ?? [];
}

// mkdir 新建目录。
export function mkdir(volume: string, dir: string, name: string): Promise<{ path: string }> {
  return request('/api/files/mkdir', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ vol: volume, dir, name }),
  });
}

// moveItems 把条目移动到目录；dest 非空时作为完整目标路径（改名用）。
export function moveItems(
  volume: string,
  paths: string[],
  to: string,
  dest?: string
): Promise<{ moved: number }> {
  return request('/api/files/move', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ vol: volume, paths, to, dest }),
  });
}

// removeItems 删除条目（目录递归）。
export function removeItems(volume: string, paths: string[]): Promise<{ removed: number }> {
  return request('/api/files/remove', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ vol: volume, paths }),
  });
}

// uploadFile 上传单个文件，onProgress 汇报 0..1 进度。
export function uploadFile(
  volume: string,
  dir: string,
  rel: string,
  file: File,
  onProgress?: (ratio: number) => void,
  signal?: AbortSignal
): Promise<{ path: string }> {
  return new Promise((resolve, reject) => {
    const form = new FormData();
    form.set('vol', volume);
    form.set('dir', dir);
    form.set('rel', rel);
    form.set('file', file, file.name);

    const xhr = new XMLHttpRequest();
    xhr.open('POST', '/api/files/upload');
    xhr.responseType = 'text';
    xhr.upload.onprogress = (ev) => {
      if (ev.lengthComputable && onProgress) onProgress(ev.loaded / ev.total);
    };
    xhr.onerror = () => reject(new ApiError('network error', 0));
    xhr.onabort = () => reject(new ApiError('aborted', 0));
    xhr.onload = () => {
      let payload: unknown = null;
      try {
        payload = JSON.parse(xhr.responseText);
      } catch {
        payload = null;
      }
      if (xhr.status >= 200 && xhr.status < 300) {
        resolve(payload as { path: string });
        return;
      }
      reject(new ApiError(asMessage(payload) || `upload failed (${xhr.status})`, xhr.status));
    };
    if (signal) {
      signal.addEventListener('abort', () => xhr.abort(), { once: true });
    }
    xhr.send(form);
  });
}

// getSettings 读取结构化设置。
export function getSettings(): Promise<ApiSettings> {
  return request<ApiSettings>('/api/settings');
}

// saveSettings 写入结构化设置：只提交要改的键，其它沿用配置文件原值。
export function saveSettings(values: Record<string, string>): Promise<{ saved: boolean }> {
  return request('/api/settings', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ values }),
  });
}

// getMount 读取 WebDAV 挂盘状态。
export function getMount(): Promise<ApiMount> {
  return request<ApiMount>('/api/mount');
}

// setMount 开关 WebDAV 挂盘。
export function setMount(enabled: boolean): Promise<ApiMount> {
  return request('/api/mount', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ enabled }),
  });
}

// triggerSync 立即增量同步。
export function triggerSync(): Promise<unknown> {
  return request('/api/sync', { method: 'POST' });
}

// createVolume 新建卷：后端建 Telegram 频道并写配置。
export function createVolume(
  name: string,
  title: string,
  point: string
): Promise<{ name: string; channel_id: number; chat_id: number }> {
  return request('/api/volumes', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name, title, point }),
  });
}

// deleteVolume 删除卷：删频道、移出配置、删本地索引，不可撤销。
export function deleteVolume(name: string): Promise<{ removed: boolean }> {
  return request(`/api/volumes?name=${encodeURIComponent(name)}`, { method: 'DELETE' });
}

// fileURL 返回下载/预览地址；inline 用于内嵌播放。
export function fileURL(volume: string, path: string, inline = false): string {
  const q = new URLSearchParams({ vol: volume, path });
  if (inline) q.set('inline', '1');
  return `/api/file?${q.toString()}`;
}

// thumbURL 返回缩略图地址。
export function thumbURL(volume: string, path: string): string {
  const q = new URLSearchParams({ vol: volume, path });
  return `/api/thumb?${q.toString()}`;
}

// coverURL 返回卷封面地址；卷根目录有 .cover.jpg / cover.png 时才会命中。
export function coverURL(volume: string): string {
  const q = new URLSearchParams({ vol: volume });
  return `/api/cover?${q.toString()}`;
}

// ---- 离线缓存（Service Worker 运行时缓存同名 cache）----

const OFFLINE_CACHE = 'mytgfs-files';

// cacheFile 把文件写入离线缓存；先确认缓存可用。
export async function cacheFile(url: string): Promise<void> {
  if (typeof caches === 'undefined') throw new Error('Cache API unavailable');
  const res = await fetch(url, { cache: 'no-store' });
  if (!res.ok) throw new Error(`${res.status}`);
  const cache = await caches.open(OFFLINE_CACHE);
  await cache.put(new Request(url), res);
}

// uncacheFile 从离线缓存移除。
export async function uncacheFile(url: string): Promise<void> {
  if (typeof caches === 'undefined') return;
  const cache = await caches.open(OFFLINE_CACHE);
  await cache.delete(new Request(url));
}

// isCached 判断某个地址是否离线可用。
export async function isCached(url: string): Promise<boolean> {
  if (typeof caches === 'undefined') return false;
  const cache = await caches.open(OFFLINE_CACHE);
  return (await cache.match(new Request(url))) !== undefined;
}
