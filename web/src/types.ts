export type ConnectionStatus = 'connected' | 'connecting' | 'reconnecting' | 'invalid_credentials';

export type IndexStatus = 'ready' | 'rebuilding';

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
  /** 频道头像版本（photo id）；空串表示频道没有头像 */
  cover?: string;
}

export type TransferDirection = 'upload' | 'download';
export type TransferState = 'transferring' | 'queued' | 'paused' | 'completed' | 'failed';

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
  opKind?: 'upload' | 'download' | 'mkdir' | 'rename' | 'remove' | 'copy';
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
export type NavTab = 'overview' | 'files' | 'settings' | 'downloads';
// 语言包目录里的文件名即 Locale；新增语言只丢 JSON，不改类型
export type Locale = string;
export type ThemeMode = 'light' | 'dark';

export type ConfigErrorCode =
  | 'SYNTAX_MISSING_EQ'
  | 'INVALID_HOST'
  | 'INVALID_PORT'
  | 'INVALID_SYNC_INTERVAL'
  | 'INVALID_DL_CONN'
  | 'INVALID_UL_CONC'
  | 'INVALID_CHUNK_SIZE'
  | 'INVALID_BOOL';
