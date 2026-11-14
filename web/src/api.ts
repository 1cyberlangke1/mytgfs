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

export interface ApiAutostart {
  enabled: boolean;
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
  caps?: {
    createVolume?: boolean;
    deleteVolume?: boolean;
    attachVolume?: boolean;
    listChannels?: boolean;
    login?: boolean;
    autostart?: boolean;
  };
  status?: {
    state?: string;
    state_code?: string;
    transfers?: ApiTransfer[];
    queue?: ApiQueueItem[];
  };
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

// getAutostart 读取开机自启状态（系统注册项）。
export function getAutostart(): Promise<ApiAutostart> {
  return request<ApiAutostart>('/api/autostart');
}

// setAutostart 开启或关闭开机自启。
export function setAutostart(enabled: boolean): Promise<ApiAutostart> {
  return request('/api/autostart', {
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
  title: string
): Promise<{ name: string; channel_id: number; chat_id: number }> {
  return request('/api/volumes', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name, title }),
  });
}

// listChannels 列出 user 账号里可管理的频道，供「添加已有频道」下拉使用。
//
// 输入：无；输出：频道数组（chat_id 已转成 -100... 形式）
// 预期：!user 会话时抛 ApiError，调用方显示原因
export function listChannels(): Promise<{
  channels: {
    id: number;
    chat_id: number;
    title: string;
    username: string;
    creator: boolean;
    admin: boolean;
  }[];
}> {
  return request('/api/channels');
}

// attachVolume 把已有频道挂成卷：后端只读校验后写配置。
export function attachVolume(
  name: string,
  chatId: number
): Promise<{ name: string; channel_id: number; chat_id: number; title: string }> {
  return request('/api/volumes/attach', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name, chat_id: chatId }),
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

// ---- 下载目录（服务端落盘）----

// ApiDownload 是下载目录里的一个文件。
export interface ApiDownload {
  name: string;
  size: number;
  mod: string;
}

// ApiDownloads 是目录路径与文件列表。
export interface ApiDownloads {
  dir: string;
  files: ApiDownload[];
}

// getDownloads 列出已下载文件。
export function getDownloads(): Promise<ApiDownloads> {
  return request<ApiDownloads>('/api/downloads');
}

// downloadToDir 让服务端把卷内文件复制到下载目录。
export function downloadToDir(
  volume: string,
  path: string
): Promise<{ dir: string; saved: ApiDownload }> {
  return request('/api/downloads', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ vol: volume, path }),
  });
}

// removeDownload 删除本地副本。
export function removeDownload(name: string): Promise<{ removed: boolean }> {
  return request(`/api/downloads?name=${encodeURIComponent(name)}`, { method: 'DELETE' });
}

// openDownloads 用系统文件管理器打开下载目录。
export function openDownloads(): Promise<{ opened: boolean }> {
  return request('/api/downloads/open', { method: 'POST' });
}

// ApiCredentials 是凭证与登录态：只有「有没有」，没有密钥内容。
export interface ApiCredentials {
  api_id: number;
  api_hash_set: boolean;
  bot_token_set: boolean;
  bot_session: boolean;
  user_session: boolean;
}

// ApiIdentity 是登录后的账号身份。
export interface ApiIdentity {
  id: number;
  username: string;
  name: string;
  bot: boolean;
}

// getCredentials 读取 api_id 与「密钥已保存」标记。
export function getCredentials(): Promise<ApiCredentials> {
  return request<ApiCredentials>('/api/credentials');
}

// saveCredentials 保存 api_id / api_hash / bot token；空字段表示不修改。
export function saveCredentials(body: {
  api_id?: number;
  api_hash?: string;
  bot_token?: string;
}): Promise<{ status: ApiCredentials; restart_required: boolean }> {
  return request('/api/credentials', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
}

// loginBot 用 bot token 登录；token 为空时用已保存的 token。
export function loginBot(token: string): Promise<{ user: ApiIdentity; restart_required: boolean }> {
  return request('/api/login/bot', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ token }),
  });
}

// startUserLogin 给手机号发验证码，返回后续要带回来的 phone_code_hash。
export function startUserLogin(
  phone: string
): Promise<{ phone_code_hash: string; timeout: number }> {
  return request('/api/login/user/start', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ phone }),
  });
}

// finishUserLogin 提交验证码；need_password 为 true 时带 password 再调一次。
export function finishUserLogin(body: {
  phone: string;
  code: string;
  phone_code_hash: string;
  password?: string;
}): Promise<{ need_password: boolean; user: ApiIdentity }> {
  return request('/api/login/user/code', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
}

// getIdentity 读取已保存会话的账号身份。
export function getIdentity(kind: 'bot' | 'user'): Promise<{ user: ApiIdentity }> {
  return request(`/api/identity?kind=${kind}`);
}
