// 面板与本地 Go 服务之间的 JSON API：传输层 + 数据结构 + 各域端点。
//
// 所有请求同源（桌面壳走 webview，网页版走 127.0.0.1），没有鉴权头；
// 错误统一抛 ApiError，由调用方转成 toast。安卓壳的 shouldInterceptRequest
// 拿不到请求体也会丢 query 串，因此安卓上所有 /api/* 经 Wails 服务方法
// mytgfs/internal/web.Bridge.Do 转发，调用方仍按 fetch 语义使用。
import { isAndroid, openNativeFile } from './platform';
import { wailsRuntime } from './runtime';
import { makeVideoThumb } from './utils/videoThumb';

/** ApiError 携带后端返回的可读原因与 HTTP 状态码。 */
class ApiError extends Error {
  constructor(
    message: string,
    readonly status: number
  ) {
    super(message);
    this.name = 'ApiError';
  }
}

/** asMessage 兼容两种错误体：{error:{message}} 与 {error:"..."}。 */
function asMessage(payload: unknown): string {
  if (!payload || typeof payload !== 'object') return '';
  const err = (payload as { error?: unknown }).error;
  if (!err) return '';
  if (typeof err === 'string') return err;
  if (typeof err === 'object' && err && 'message' in err) return String((err as { message: unknown }).message);
  return '';
}

interface BridgeReply {
  status?: number;
  type?: string;
  body?: string;
}

// bridgeFetch 经 Wails 服务方法执行一次控制面请求；body 必须是字符串。
async function bridgeFetch(path: string, init?: RequestInit): Promise<Response> {
  const call = wailsRuntime()?.Call?.ByName;
  if (!call) throw new ApiError('desktop runtime unavailable', 0);
  const method = (init?.method ?? 'GET').toUpperCase();
  const raw = init?.body ?? null;
  if (raw !== null && typeof raw !== 'string') throw new ApiError('binary body must not use the text bridge', 0);
  const pending = call('mytgfs/internal/web.Bridge.Do', { method, path, body: raw ?? '' }) as Promise<unknown> & {
    cancel?: () => void;
  };
  const signal = init?.signal;
  if (signal) {
    if (signal.aborted) {
      pending.cancel?.();
      throw new ApiError('aborted', 0);
    }
    signal.addEventListener('abort', () => pending.cancel?.(), { once: true });
  }
  try {
    const reply = (await pending) as BridgeReply;
    const status = reply.status ?? 200;
    const headers = new Headers();
    if (reply.type) headers.set('Content-Type', reply.type);
    const text = reply.body ?? '';
    const noBody = status === 204 || status === 205 || status === 304 || text === '';
    return new Response(noBody ? null : text, { status, headers });
  } catch (err) {
    if (signal?.aborted) throw new ApiError('aborted', 0);
    throw err;
  }
}

/** rawFetch 是 fetch 的平台分派：安卓走文本桥，其余照旧。 */
function rawFetch(path: string, init?: RequestInit): Promise<Response> {
  return isAndroid ? bridgeFetch(path, init) : fetch(path, init);
}

/** request 发一次 JSON 请求并解析响应；非 2xx 抛 ApiError。 */
async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await rawFetch(path, init);
  const text = await res.text();
  let payload: unknown = null;
  if (text) {
    try {
      payload = JSON.parse(text);
    } catch {
      payload = null;
    }
  }
  if (!res.ok) throw new ApiError(asMessage(payload) || `${res.status} ${res.statusText}`, res.status);
  return payload as T;
}

const json = (method: string, body: unknown, signal?: AbortSignal): RequestInit => ({
  method,
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify(body),
  signal,
});

// ---- 数据结构（与 Go 侧 JSON 字段一一对应）----
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
interface ApiVolume {
  id: string;
  name: string;
  title?: string;
  entries: number;
  used: number;
  cover?: string;
}
export interface ApiTransfer {
  path: string;
  done: number;
  total: number;
  kind?: string;
}
export interface ApiQueueItem {
  id: number;
  volume: string;
  kind: string;
  name: string;
  state: string;
  attempt: number;
  error?: string;
  done?: number;
  size?: number;
}
export interface ApiState {
  volumes: ApiVolume[];
  login_ready?: boolean;
  caps?: {
    createVolume?: boolean;
    deleteVolume?: boolean;
    attachVolume?: boolean;
    listChannels?: boolean;
    login?: boolean;
    autostart?: boolean;
    cover?: boolean;
    rebuild?: boolean;
  };
  status?: { state?: string; state_code?: string; transfers?: ApiTransfer[]; queue?: ApiQueueItem[] };
}
interface ApiSettingDef {
  key: string;
  kind: 'string' | 'int' | 'bool';
}
export interface ApiSettings {
  schema: ApiSettingDef[];
  values: Record<string, string>;
  rawConfigText: string;
}
export interface ApiAutostart {
  enabled: boolean;
}
interface ApiDownload {
  name: string;
  size: number;
  mod: string;
  path: string;
}
export interface ApiDownloadTask {
  id: string;
  name: string;
  vol: string;
  path: string;
  size: number;
  done: number;
  state: 'running' | 'paused' | 'failed';
  error?: string;
}
export interface ApiDownloads {
  dir: string;
  files: ApiDownload[];
  tasks?: ApiDownloadTask[];
}
export interface ApiCredentials {
  api_id: number;
  api_hash_set: boolean;
  bot_token_set: boolean;
  bot_session: boolean;
  user_session: boolean;
}
export interface ApiIdentity {
  id: number;
  username: string;
  name: string;
  bot: boolean;
}

// ---- 状态与同步 ----
export const getState = () => request<ApiState>('/api/state');
export const rebuildVolume = (volume: string, signal?: AbortSignal) =>
  request<{ files: number }>(`/api/rebuild?vol=${encodeURIComponent(volume)}`, { method: 'POST', signal });

// ---- 文件 ----
export async function listFiles(volume: string, all = false, dir = '/'): Promise<ApiFile[]> {
  const q = new URLSearchParams({ vol: volume });
  if (all) q.set('all', '1');
  else q.set('dir', dir);
  const res = await request<{ files: ApiFile[] }>(`/api/files?${q.toString()}`);
  return res.files ?? [];
}
export const mkdir = (volume: string, dir: string, name: string) =>
  request<{ path: string }>('/api/files/mkdir', json('POST', { vol: volume, dir, name }));
export const moveItems = (volume: string, paths: string[], to: string, dest?: string) =>
  request<{ moved: number }>('/api/files/move', json('POST', { vol: volume, paths, to, dest }));
export const removeItems = (volume: string, paths: string[]) =>
  request<{ removed: number }>('/api/files/remove', json('POST', { vol: volume, paths }));
export const fileURL = (volume: string, path: string, inline = false) => {
  const q = new URLSearchParams({ vol: volume, path });
  if (inline) q.set('inline', '1');
  return `/api/file?${q.toString()}`;
};
export const thumbURL = (volume: string, path: string) =>
  `/api/thumb?${new URLSearchParams({ vol: volume, path }).toString()}`;
export async function uploadPoster(volume: string, path: string, poster: Blob): Promise<void> {
  if (isAndroid) return;
  const q = new URLSearchParams({ vol: volume, path });
  const res = await rawFetch(`/api/poster?${q.toString()}`, {
    method: 'POST',
    headers: { 'Content-Type': 'image/jpeg' },
    body: poster,
  });
  if (!res.ok) throw new ApiError((await res.text()).trim() || res.statusText, res.status);
}

// ---- 上传（原始字节分片；桌面壳收不到 multipart 体）----
const UPLOAD_CHUNK = 4 << 20;

/** UploadPaused 表示上传被用户暂停：服务端会话与已传字节仍在。 */
export class UploadPaused extends Error {
  constructor(
    readonly id: string,
    readonly offset: number
  ) {
    super('paused');
    this.name = 'UploadPaused';
  }
}
export interface UploadResume {
  id: string;
  offset: number;
}

function putChunk(
  id: string,
  offset: number,
  body: ArrayBuffer,
  total: number,
  onProgress?: (ratio: number) => void,
  signal?: AbortSignal
): Promise<number> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open('PUT', `/api/upload/chunk?${new URLSearchParams({ id, offset: String(offset) }).toString()}`);
    xhr.setRequestHeader('Content-Type', 'application/octet-stream');
    xhr.responseType = 'text';
    xhr.upload.onprogress = (ev) => {
      if (ev.lengthComputable && onProgress) onProgress(Math.min(1, (offset + ev.loaded) / Math.max(1, total)));
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
        const next = (payload as { offset?: number } | null)?.offset;
        resolve(typeof next === 'number' ? next : offset + body.byteLength);
        return;
      }
      reject(new ApiError(asMessage(payload) || `upload failed (${xhr.status})`, xhr.status));
    };
    if (signal) {
      if (signal.aborted) {
        reject(new ApiError('aborted', 0));
        return;
      }
      signal.addEventListener('abort', () => xhr.abort(), { once: true });
    }
    xhr.send(body);
  });
}

export const discardUpload = (id: string): Promise<void> =>
  request(`/api/upload?id=${encodeURIComponent(id)}`, { method: 'DELETE' })
    .then(() => undefined)
    .catch(() => undefined);

async function putUploadThumb(id: string, blob: Blob): Promise<void> {
  await request(`/api/upload/thumb?id=${encodeURIComponent(id)}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'image/jpeg' },
    body: await blob.arrayBuffer(),
  });
}

export async function uploadFile(
  volume: string,
  dir: string,
  rel: string,
  name: string,
  file: File,
  onProgress?: (ratio: number) => void,
  signal?: AbortSignal,
  resume?: UploadResume,
  onSession?: (id: string) => void
): Promise<{ path: string; job: number }> {
  const session = resume?.id
    ? { id: resume.id }
    : await request<{ id: string }>('/api/upload/init', json('POST', { vol: volume, dir, rel, name, size: file.size }));
  onSession?.(session.id);
  // 抽帧与分片并行：抽帧慢不挡内容上传，提交前再等它一次
  const thumbTask = resume?.id
    ? Promise.resolve()
    : makeVideoThumb(file).then((blob) => (blob ? putUploadThumb(session.id, blob) : undefined));
  let offset = resume?.offset ?? 0;
  try {
    while (offset < file.size) {
      const end = Math.min(offset + UPLOAD_CHUNK, file.size);
      const bytes = await file.slice(offset, end).arrayBuffer();
      offset = await putChunk(session.id, offset, bytes, file.size, onProgress, signal);
    }
    await thumbTask;
    const done = await request<{ path: string; job?: number }>(`/api/upload/commit?id=${encodeURIComponent(session.id)}`, { method: 'POST' });
    onProgress?.(1);
    return { path: done?.path ?? '', job: done?.job ?? 0 };
  } catch (err) {
    void thumbTask.catch(() => undefined);
    if (err instanceof ApiError && err.status === 0 && err.message === 'aborted') throw new UploadPaused(session.id, offset);
    // 服务端会话过期（重启/超时）：自动重开一次
    if (err instanceof ApiError && err.status === 404 && (resume || offset > 0)) {
      return uploadFile(volume, dir, rel, name, file, onProgress, signal);
    }
    void discardUpload(session.id);
    throw err;
  }
}

// ---- 卷管理 ----
export const createVolume = (name: string, title: string, signal?: AbortSignal) =>
  request<{ name: string; channel_id: number; chat_id: number }>('/api/volumes', json('POST', { name, title }, signal));
export const listChannels = () =>
  request<{ channels: { id: number; chat_id: number; title: string; username: string; creator: boolean; admin: boolean }[] }>(
    '/api/channels'
  );
export const attachVolume = (name: string, chatId: number) =>
  request<{ name: string; channel_id: number; chat_id: number; title: string }>(
    '/api/volumes/attach',
    json('POST', { name, chat_id: chatId })
  );
export const deleteVolume = (name: string) =>
  request<{ removed: boolean }>(`/api/volumes?name=${encodeURIComponent(name)}`, { method: 'DELETE' });
export const coverURL = (volume: string, version?: string) => {
  const q = new URLSearchParams({ vol: volume });
  if (version) q.set('v', version);
  return `/api/cover?${q.toString()}`;
};
export async function setCover(volume: string, file: File): Promise<void> {
  const res = await rawFetch(`/api/cover?${new URLSearchParams({ vol: volume }).toString()}`, {
    method: 'POST',
    headers: { 'Content-Type': file.type || 'application/octet-stream' },
    body: file,
  });
  if (!res.ok) throw new ApiError((await res.text()).trim() || res.statusText, res.status);
}
export async function setVolumeTitle(volume: string, title: string): Promise<void> {
  const res = await rawFetch(`/api/cover?${new URLSearchParams({ vol: volume }).toString()}`, json('PUT', { title }));
  if (!res.ok) throw new ApiError((await res.text()).trim() || res.statusText, res.status);
}

// ---- 服务端队列 ----
export const clearQueue = () => request<{ removed: number }>('/api/queue', { method: 'DELETE' });
export const cancelQueueJob = (volume: string, id: number) =>
  request<{ cancelled: boolean }>(`/api/queue/${encodeURIComponent(volume)}/${id}`, { method: 'DELETE' });

// ---- 下载目录 ----
export const localFileURL = (name: string) => `/api/downloads/file?name=${encodeURIComponent(name)}`;
export async function openDownloadedFile(name: string): Promise<{ opened: boolean }> {
  const res = await request<{ opened: boolean; path?: string }>(`/api/downloads/open-file?name=${encodeURIComponent(name)}`, {
    method: 'POST',
  });
  if (!res.opened && isAndroid && res.path) {
    openNativeFile(res.path);
    return { opened: true };
  }
  return { opened: res.opened };
}
export const getDownloads = () => request<ApiDownloads>('/api/downloads');
export const startDownload = (volume: string, path: string) =>
  request<{ task: ApiDownloadTask }>('/api/downloads', json('POST', { vol: volume, path }));
export const pauseDownload = (id: string) =>
  request<{ ok: boolean }>('/api/downloads/task', json('POST', { id, action: 'pause' }));
export const resumeDownload = (id: string) =>
  request<{ ok: boolean }>('/api/downloads/task', json('POST', { id, action: 'resume' }));
export const cancelDownload = (id: string) =>
  request<{ removed: boolean }>(`/api/downloads?id=${encodeURIComponent(id)}`, { method: 'DELETE' });
export const removeDownload = (name: string) =>
  request<{ removed: boolean }>(`/api/downloads?name=${encodeURIComponent(name)}`, { method: 'DELETE' });
export const openDownloads = () => request<{ opened: boolean }>('/api/downloads/open', { method: 'POST' });

// ---- 设置与自启 ----
export const getSettings = () => request<ApiSettings>('/api/settings');
export const saveSettings = (values: Record<string, string>) =>
  request<{ saved: boolean }>('/api/settings', json('PUT', { values }));
export const getAutostart = () => request<ApiAutostart>('/api/autostart');
export const setAutostart = (enabled: boolean) => request<ApiAutostart>('/api/autostart', json('POST', { enabled }));

// ---- 凭证与登录 ----
export const getCredentials = () => request<ApiCredentials>('/api/credentials');
export const saveCredentials = (body: { api_id?: number; api_hash?: string; bot_token?: string }) =>
  request<{ status: ApiCredentials; restart_required: boolean }>('/api/credentials', json('PUT', body));
export const loginBot = (token: string) =>
  request<{ user: ApiIdentity; restart_required: boolean }>('/api/login/bot', json('POST', { token }));
export const startUserLogin = (phone: string) =>
  request<{ phone_code_hash: string; timeout: number }>('/api/login/user/start', json('POST', { phone }));
export const finishUserLogin = (body: { phone: string; code: string; phone_code_hash: string; password?: string }) =>
  request<{ need_password: boolean; user: ApiIdentity }>('/api/login/user/code', json('POST', body));
export const getIdentity = (kind: 'bot' | 'user') =>
  request<{ user: ApiIdentity }>(`/api/identity?kind=${kind}`);

// ---- 安卓原生选择器桥（壳注入 window.mytgfsNative）----
export interface MytgfsNativeBridge {
  pickFileImport?: (this: MytgfsNativeBridge, v: string, d: string) => void;
  pickFolderImport?: (this: MytgfsNativeBridge, v: string, d: string) => void;
  pickCover?: (this: MytgfsNativeBridge, vol: string) => void;
}

// nativeImport 走安卓原生选择器：壳用 SAF 打开 fd 交给服务端接管，字节不进 JS。
function nativeImport(
  zone: 'file' | 'folder',
  volume: string,
  dir: string
): Promise<{ uploaded: number; failed: number; failedNames: string[] }> {
  return new Promise((resolve, reject) => {
    const w = window as unknown as {
      mytgfsNative?: MytgfsNativeBridge;
      _mytgfsFileImport?: (uploaded: number, failed: number, failedNames?: string[]) => void;
      _mytgfsFolderImport?: (uploaded: number, failed: number, failedNames?: string[]) => void;
    };
    const native = w.mytgfsNative;
    const hasMethod = zone === 'file' ? typeof native?.pickFileImport === 'function' : typeof native?.pickFolderImport === 'function';
    if (!native || !hasMethod) {
      reject(new ApiError('native picker unavailable', 0));
      return;
    }
    const done = (uploaded: number, failed: number, failedNames?: string[]) =>
      resolve({ uploaded, failed, failedNames: failedNames ?? [] });
    if (zone === 'file') {
      w._mytgfsFileImport = (uploaded, failed, failedNames) => {
        delete w._mytgfsFileImport;
        done(uploaded, failed, failedNames);
      };
      native.pickFileImport!(volume, dir);
    } else {
      w._mytgfsFolderImport = (uploaded, failed, failedNames) => {
        delete w._mytgfsFolderImport;
        done(uploaded, failed, failedNames);
      };
      native.pickFolderImport!(volume, dir);
    }
  });
}
export const importFileNative = (volume: string, dir: string) => nativeImport('file', volume, dir);
export const importFolderNative = (volume: string, dir: string) => nativeImport('folder', volume, dir);
export function pickCoverNative(volume: string): Promise<boolean> {
  return new Promise((resolve, reject) => {
    const w = window as unknown as {
      mytgfsNative?: MytgfsNativeBridge;
      _mytgfsCoverPicked?: (ok: boolean) => void;
    };
    const native = w.mytgfsNative;
    if (typeof native?.pickCover !== 'function') {
      reject(new ApiError('native cover picker unavailable', 0));
      return;
    }
    w._mytgfsCoverPicked = (ok) => {
      delete w._mytgfsCoverPicked;
      resolve(ok);
    };
    native.pickCover(volume);
  });
}
