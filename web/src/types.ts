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
  alias: string;
  aliasEn?: string;
  channelHint: string;
  description: string;
  descriptionEn?: string;
  createdAt: string;
  /** 卷根目录是否存在 cover.jpg/.cover.png，只有为 true 才请求封面图 */
  hasCover?: boolean;
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
}

export interface AppSettingDef {
  key: string;
  kind: 'string' | 'int' | 'bool';
  min?: number;
  max?: number;
  options?: string[];
}

export interface AppSettings {
  listenHost: '127.0.0.1';
  panelPort: number;
  autoSyncIntervalSec: number;
  downloadConnections: number;
  uploadConcurrency: number;
  readChunkSizeMb: number;
  pwaOfflineMaxFileMb: number;
  webdavEnabled: boolean;
  webdavPath: string;
  /** 服务端 config.Schema() 返回的可编辑键，面板据此动态渲染 */
  schema: AppSettingDef[];
  /** 配置文件里的原始字符串值，键为 section.key */
  values: Record<string, string>;
}

export interface OfflineSavedFile {
  fileId: string;
  volumeId: string;
  name: string;
  size: number;
  category: FileCategory;
  savedAt: string;
  thumbnailUrl?: string;
  previewUrl?: string;
}

export type SortField = 'name' | 'size' | 'updatedAt';
export type SortOrder = 'asc' | 'desc';
export type NavTab = 'overview' | 'files' | 'mount' | 'settings' | 'offline';
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
  | 'INVALID_BOOL'
  | 'INVALID_PWA_LIMIT';
