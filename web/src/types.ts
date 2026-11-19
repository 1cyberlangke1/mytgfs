export type ConnectionStatus = 'connected' | 'connecting' | 'reconnecting' | 'invalid_credentials';
export type FileCategory = 'folder' | 'image' | 'video' | 'audio' | 'pdf' | 'archive' | 'document';

export interface FileItem {
  id: string;
  volumeId: string;
  parentId: string | null; // null means root of the volume
  name: string;
  isDir: boolean;
  size: number; // in bytes (0 for directories, or calculated dynamically)
  updatedAt: string; // ISO string
  category: FileCategory;
  mimeType?: string;
  thumbnailUrl?: string;
  previewUrl?: string;
  durationSec?: number;
  pageCount?: number;
  tgMessageIds?: number[];
  chunksCount?: number;
}

export interface VolumeInfo {
  id: string;
  name: string;
  /** 频道标题（Telegram 侧显示名）；空表示读不到，显示回落到 name */
  title?: string;
  /** 频道头像版本（photo id）；空串表示频道没有头像 */
  cover?: string;
}

export type TransferDirection = 'upload' | 'download';
// cancelled 只出现在服务端队列任务上：用户主动取消后落这个终态，
// 与 failed（错误）区分开，界面不给重试入口
export type TransferState =
  | 'transferring'
  | 'queued'
  | 'paused'
  | 'completed'
  | 'failed'
  | 'cancelled';

export interface TransferTask {
  id: string;
  fileName: string;
  volumeId: string;
  targetParentId: string | null;
  direction: TransferDirection;
  size: number;
  transferredBytes: number;
  state: TransferState;
  speedBps: number;
  isChunkedResume: boolean;
  chunkSize: number;
  currentChunk: number;
  totalChunks: number;
  errorMessage?: string;
  createdAt: string;
  category?: FileCategory;
  // opKind 是服务端队列里的操作类型；前端本地上传任务固定为 upload
  opKind?: 'upload' | 'download' | 'mkdir' | 'rename' | 'remove' | 'copy' | 'other';
  /** 同一次「上传文件/上传文件夹」的批次号，用于汇总整批进度 */
  batchId?: string;
  /** 服务端队列任务所属卷名；本地任务为空。取消接口需要它 */
  queueVolume?: string;
  /** 服务端队列任务号；本地任务为空 */
  queueJobId?: number;
  /** 上传的两段进度：local=浏览器到服务端，remote=服务端到 Telegram */
  phase?: 'local' | 'remote';
}

export interface AppSettingDef {
  key: string;
  kind: 'string' | 'int' | 'bool';
  min?: number;
  max?: number;
  options?: string[];
}

export interface AppSettings {
  /** 服务端 config.Schema() 返回的可编辑键，面板据此动态渲染 */
  schema: AppSettingDef[];
  /** 服务端返回的真实生效值（配置值或运行时默认值），键为 section.key */
  values: Record<string, string>;
}

export interface DownloadItem {
  name: string;
  size: number;
  /** RFC3339 文本，按本地时区展示 */
  mod: string;
  /** 本地绝对路径；安卓导出到系统下载目录时用它定位沙盒文件 */
  path: string;
}

/** 下载任务状态：完成的任务会直接进入文件列表 */
export type DownloadTaskState = 'running' | 'paused' | 'failed';

export interface DownloadTask {
  id: string;
  name: string;
  vol: string;
  path: string;
  size: number;
  done: number;
  state: DownloadTaskState;
  error?: string;
}

export type SortField = 'name' | 'size' | 'updatedAt';
export type SortOrder = 'asc' | 'desc';
/** ManageableChannel 是 user 会话可管理的 Telegram 频道（建卷/挂卷用）。 */
export interface ManageableChannel {
  id: number;
  title: string;
  chat_id: number;
  username: string;
  creator: boolean;
  admin: boolean;
}
export type NavTab = 'overview' | 'files' | 'settings' | 'downloads';
// 语言包目录里的文件名即 Locale；新增语言只丢 JSON，不改类型
export type Locale = string;
// ThemeMode 是外观偏好：system 表示跟随操作系统的深浅色设置
export type ThemeMode = 'light' | 'dark' | 'system';

