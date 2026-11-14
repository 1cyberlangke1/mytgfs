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
  /** 频道头像版本（photo id）；空串表示没有头像 */
  cover?: string;
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
  volume: string;
  kind: string;
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
    cover?: boolean;
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

// UPLOAD_CHUNK 是分片大小。内容一律以原始字节发送：
// 桌面壳的 Wails AssetServer 收不到 FormData/Blob 请求体（官方 issue #5845、
// #6068），multipart 上传在桌面端会变成 0 字节文件，所以这里走分片接口。
const UPLOAD_CHUNK = 4 << 20;

// putChunk 发送一段原始字节，返回服务端累计偏移。
//
// 输入：会话 id、该段起始偏移、这一段字节、文件总大小
// 输出：Promise<number> 已收字节数
// 预期：409 表示偏移对不上，交给调用方报错；进度按整文件比例上报
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
    const q = new URLSearchParams({ id, offset: String(offset) });
    xhr.open('PUT', `/api/upload/chunk?${q.toString()}`);
    xhr.setRequestHeader('Content-Type', 'application/octet-stream');
    xhr.responseType = 'text';
    xhr.upload.onprogress = (ev) => {
      if (ev.lengthComputable && onProgress) {
        onProgress(Math.min(0.99, (offset + ev.loaded) / Math.max(1, total)));
      }
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

// UploadPaused 表示上传被用户暂停：服务端会话与已传字节仍在。
//
// 输入：会话 id、已传字节数
// 输出：可由调用方保存后作为 resume 参数再次上传
// 预期：暂停不删除会话，继续时从 offset 接着传
export class UploadPaused extends Error {
  constructor(
    readonly id: string,
    readonly offset: number
  ) {
    super('paused');
    this.name = 'UploadPaused';
  }
}

/** UploadResume 是续传所需的断点信息。 */
export interface UploadResume {
  id: string;
  offset: number;
}

// discardUpload 丢弃服务端会话与其临时文件。
//
// 输入：会话 id
// 输出：Promise；失败静默（会话也会被空闲回收）
// 预期：取消上传时立即释放磁盘，不等一小时超时
export function discardUpload(id: string): Promise<void> {
  return request(`/api/upload?id=${encodeURIComponent(id)}`, { method: 'DELETE' })
    .then(() => undefined)
    .catch(() => undefined);
}

// uploadFile 上传单个文件，onProgress 汇报 0..1 进度。
//
// 输入：卷、目标目录、相对路径、文件、进度回调、取消信号、断点
// 输出：写入后的卷内路径
// 预期：
//   - 取消时抛 UploadPaused，会话保留，调用方可带断点继续
//   - 会话已失效（404）时自动从头重传一次，而不是让用户手点
//   - 其它错误丢弃服务端会话，不留半份临时文件
export async function uploadFile(
  volume: string,
  dir: string,
  rel: string,
  file: File,
  onProgress?: (ratio: number) => void,
  signal?: AbortSignal,
  resume?: UploadResume
): Promise<{ path: string }> {
  const session = resume?.id
    ? { id: resume.id }
    : await request<{ id: string }>('/api/upload/init', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ vol: volume, dir, rel, name: file.name, size: file.size }),
      });
  let offset = resume?.offset ?? 0;
  try {
    while (offset < file.size) {
      const end = Math.min(offset + UPLOAD_CHUNK, file.size);
      // 取原始 ArrayBuffer 而不是 Blob：Blob 请求体在桌面壳里同样不可靠
      const bytes = await file.slice(offset, end).arrayBuffer();
      offset = await putChunk(session.id, offset, bytes, file.size, onProgress, signal);
    }
    const done = await request<{ path: string }>(
      `/api/upload/commit?id=${encodeURIComponent(session.id)}`,
      { method: 'POST' }
    );
    onProgress?.(1);
    return { path: done?.path ?? '' };
  } catch (err) {
    if (err instanceof ApiError && err.status === 0 && err.message === 'aborted') {
      throw new UploadPaused(session.id, offset);
    }
    if (err instanceof ApiError && err.status === 404 && (resume || offset > 0)) {
      // 服务端会话过期（重启/超时）：自动重开一次
      return uploadFile(volume, dir, rel, file, onProgress, signal);
    }
    void request(`/api/upload?id=${encodeURIComponent(session.id)}`, { method: 'DELETE' }).catch(
      () => undefined
    );
    throw err;
  }
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

// clearQueue 删除服务端已结束（成功/失败）的任务记录。
//
// 输出：删除条数
// 预期：排队中与执行中的任务不受影响；前端收到 status 推送后重取快照
export function clearQueue(): Promise<{ removed: number }> {
  return request('/api/queue', { method: 'DELETE' });
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

// coverURL 返回频道头像地址；version 是 photo id，换头像后 URL 自然失效。
export function coverURL(volume: string, version?: string): string {
  const q = new URLSearchParams({ vol: volume });
  if (version) q.set('v', version);
  return `/api/cover?${q.toString()}`;
}

// setCover 上传一张原图并写成频道头像（服务端裁方压缩）。
export async function setCover(volume: string, file: File): Promise<void> {
  const q = new URLSearchParams({ vol: volume });
  const res = await fetch(`/api/cover?${q.toString()}`, {
    method: 'POST',
    headers: { 'Content-Type': file.type || 'application/octet-stream' },
    body: file,
  });
  if (!res.ok) {
    throw new ApiError((await res.text()).trim() || res.statusText, res.status);
  }
}

// ---- 下载目录（服务端落盘）----

// ApiDownload 是下载目录里的一个文件。
export interface ApiDownload {
  name: string;
  size: number;
  mod: string;
}

// ApiDownloadTask 是一次进行中的下载。
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

// ApiDownloads 是目录路径、文件列表与进行中的任务。
export interface ApiDownloads {
  dir: string;
  files: ApiDownload[];
  tasks?: ApiDownloadTask[];
}

// localFileURL 返回下载目录里某个文件的直链。
//
// 输入：name 下载目录内的文件名
// 输出：可喂给 img/video/audio/iframe 的 URL
// 预期：服务端走 ServeContent，支持 Range
export function localFileURL(name: string): string {
  return `/api/downloads/file?name=${encodeURIComponent(name)}`;
}

// openDownloadedFile 用系统默认程序打开已下载文件。
//
// 输入：name 文件名
// 输出：{opened:true}
// 预期：桌面壳与网页版走同一接口；失败由调用方提示
export function openDownloadedFile(name: string): Promise<{ opened: boolean }> {
  return request(`/api/downloads/open-file?name=${encodeURIComponent(name)}`, { method: 'POST' });
}

// getDownloads 列出已下载文件与进行中的任务。
export function getDownloads(): Promise<ApiDownloads> {
  return request<ApiDownloads>('/api/downloads');
}

// startDownload 启动后台下载并立即返回任务。
//
// 输出：202 的任务快照；进度靠事件推送，不再阻塞请求
export function startDownload(
  volume: string,
  path: string
): Promise<{ task: ApiDownloadTask }> {
  return request('/api/downloads', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ vol: volume, path }),
  });
}

// pauseDownload / resumeDownload 控制单个下载任务。
export function pauseDownload(id: string): Promise<{ ok: boolean }> {
  return request('/api/downloads/task', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ id, action: 'pause' }),
  });
}

export function resumeDownload(id: string): Promise<{ ok: boolean }> {
  return request('/api/downloads/task', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ id, action: 'resume' }),
  });
}

// cancelDownload 取消任务并删除临时文件。
export function cancelDownload(id: string): Promise<{ removed: boolean }> {
  return request(`/api/downloads?id=${encodeURIComponent(id)}`, { method: 'DELETE' });
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
