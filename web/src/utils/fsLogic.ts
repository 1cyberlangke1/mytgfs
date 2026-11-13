import {
  AppSettings,
  ConfigErrorCode,
  FileCategory,
  FileItem,
  Locale,
  SortField,
  SortOrder,
  VolumeInfo,
} from '../types';
import { getLocaleMeta } from './i18n';

export const MAX_SINGLE_FILE_BYTES = 2 * 1024 * 1024 * 1024; // 2 GB strict limit
export const CHUNK_RESUME_THRESHOLD_BYTES = 8 * 1024 * 1024; // 8 MB threshold for chunked resumable upload
export const DEFAULT_UPLOAD_CHUNK_BYTES = 4 * 1024 * 1024; // 4 MB per chunk when > 8MB

/**
 * Formats byte count into human-readable string with locale-aware numbers and units.
 * When `locale` is omitted, defaults to standard English/ASCII dot formatting for unit tests.
 */
export function formatBytes(bytes: number, decimals = 2, locale?: Locale): string {
  const units = locale ? getLocaleMeta(locale).byteUnits : ['B', 'KB', 'MB', 'GB', 'TB'];
  if (!Number.isFinite(bytes) || bytes <= 0) return `0 ${units[0]}`;
  const k = 1024;
  const dm = decimals < 0 ? 0 : decimals;
  const i = Math.min(Math.floor(Math.log(bytes) / Math.log(k)), units.length - 1);
  const val = bytes / Math.pow(k, i);

  if (!locale) {
    return `${i === 0 ? Math.round(val) : val.toFixed(dm)} ${units[i]}`;
  }

  const meta = getLocaleMeta(locale);
  try {
    const formatted = new Intl.NumberFormat(meta.htmlLang, {
      minimumFractionDigits: i === 0 ? 0 : dm,
      maximumFractionDigits: i === 0 ? 0 : dm,
    }).format(i === 0 ? Math.round(val) : val);
    return `${formatted} ${units[i]}`;
  } catch {
    return `${i === 0 ? Math.round(val) : val.toFixed(dm)} ${units[i]}`;
  }
}

/**
 * Determines file category from filename or MIME type.
 */
export function inferFileCategory(fileName: string, isDir = false): FileCategory {
  if (isDir) return 'folder';
  const ext = fileName.split('.').pop()?.toLowerCase() || '';
  if (['jpg', 'jpeg', 'png', 'webp', 'gif', 'avif', 'svg', 'bmp'].includes(ext)) {
    return 'image';
  }
  if (['mp4', 'mkv', 'webm', 'mov', 'avi'].includes(ext)) {
    return 'video';
  }
  if (['mp3', 'flac', 'wav', 'ogg', 'm4a', 'aac'].includes(ext)) {
    return 'audio';
  }
  if (ext === 'pdf') {
    return 'pdf';
  }
  if (['zip', 'tar', 'gz', '7z', 'rar', 'tgz', 'xz', 'zst'].includes(ext)) {
    return 'archive';
  }
  return 'document';
}

/**
 * Validates file upload against the 2GB single-file limit and determines chunking strategy.
 */
export interface UploadValidationResult {
  valid: boolean;
  errorCode?: 'FILE_TOO_LARGE' | 'EMPTY_NAME';
  isChunkedResume: boolean;
  chunkSize: number;
  totalChunks: number;
}

export function validateAndPlanUpload(fileName: string, sizeBytes: number): UploadValidationResult {
  if (!fileName || !fileName.trim()) {
    return {
      valid: false,
      errorCode: 'EMPTY_NAME',
      isChunkedResume: false,
      chunkSize: sizeBytes,
      totalChunks: 1,
    };
  }
  if (sizeBytes > MAX_SINGLE_FILE_BYTES) {
    return {
      valid: false,
      errorCode: 'FILE_TOO_LARGE',
      isChunkedResume: false,
      chunkSize: DEFAULT_UPLOAD_CHUNK_BYTES,
      totalChunks: 0,
    };
  }
  const isChunkedResume = sizeBytes > CHUNK_RESUME_THRESHOLD_BYTES;
  const chunkSize = isChunkedResume ? DEFAULT_UPLOAD_CHUNK_BYTES : Math.max(sizeBytes, 1);
  const totalChunks = isChunkedResume ? Math.ceil(sizeBytes / DEFAULT_UPLOAD_CHUNK_BYTES) : 1;

  return {
    valid: true,
    isChunkedResume,
    chunkSize,
    totalChunks,
  };
}

/**
 * Computes recursive size and file count for a directory or volume.
 */
export function computeDirectoryStats(
  allFiles: FileItem[],
  volumeId: string,
  rootFolderId: string | null = null
): { fileCount: number; folderCount: number; totalBytes: number } {
  const volumeItems = allFiles.filter((f) => f.volumeId === volumeId);

  if (rootFolderId === null) {
    let fileCount = 0;
    let folderCount = 0;
    let totalBytes = 0;
    for (const item of volumeItems) {
      if (item.isDir) {
        folderCount++;
      } else {
        fileCount++;
        totalBytes += item.size;
      }
    }
    return { fileCount, folderCount, totalBytes };
  }

  // BFS/DFS from rootFolderId
  const childrenMap = new Map<string | null, FileItem[]>();
  for (const item of volumeItems) {
    const list = childrenMap.get(item.parentId) || [];
    list.push(item);
    childrenMap.set(item.parentId, list);
  }

  let fileCount = 0;
  let folderCount = 0;
  let totalBytes = 0;
  const stack: string[] = [rootFolderId];

  while (stack.length > 0) {
    const currentId = stack.pop()!;
    const children = childrenMap.get(currentId) || [];
    for (const child of children) {
      if (child.isDir) {
        folderCount++;
        stack.push(child.id);
      } else {
        fileCount++;
        totalBytes += child.size;
      }
    }
  }

  return { fileCount, folderCount, totalBytes };
}

/**
 * Returns all descendant IDs (including the target IDs themselves) for recursive deletion.
 */
export function collectRecursiveDeleteIds(allFiles: FileItem[], targetIds: string[]): Set<string> {
  const toDelete = new Set<string>(targetIds);
  let added = true;
  while (added) {
    added = false;
    for (const item of allFiles) {
      if (item.parentId && toDelete.has(item.parentId) && !toDelete.has(item.id)) {
        toDelete.add(item.id);
        added = true;
      }
    }
  }
  return toDelete;
}

/**
 * Checks if moving a folder into targetParentId would create a cycle (moving into itself or its own descendant).
 */
export function canMoveItemsToFolder(
  allFiles: FileItem[],
  movingIds: string[],
  targetParentId: string | null
): boolean {
  if (targetParentId === null) return true;
  const forbiddenTargets = collectRecursiveDeleteIds(allFiles, movingIds);
  return !forbiddenTargets.has(targetParentId);
}

/**
 * Sorts files with directories ALWAYS first ("目录在前"), then by the chosen field and order,
 * using locale-aware Intl.Collator (e.g. Pinyin for zh-CN, Kana for ja-JP, Cyrillic for ru-RU).
 */
export function sortFileItems(
  items: FileItem[],
  field: SortField,
  order: SortOrder,
  allFilesForDirSizes?: FileItem[],
  locale?: Locale
): FileItem[] {
  const dirSizeCache = new Map<string, number>();
  if (field === 'size' && allFilesForDirSizes) {
    for (const item of items) {
      if (item.isDir) {
        dirSizeCache.set(
          item.id,
          computeDirectoryStats(allFilesForDirSizes, item.volumeId, item.id).totalBytes
        );
      }
    }
  }

  const collatorLocale = locale ? getLocaleMeta(locale).collatorLocale : undefined;
  const collator = new Intl.Collator(collatorLocale, {
    numeric: true,
    sensitivity: 'base',
  });

  return [...items].sort((a, b) => {
    // Directories ALWAYS come before regular files
    if (a.isDir !== b.isDir) {
      return a.isDir ? -1 : 1;
    }

    let cmp = 0;
    if (field === 'name') {
      cmp = collator.compare(a.name, b.name);
    } else if (field === 'size') {
      const sizeA = a.isDir ? (dirSizeCache.get(a.id) ?? 0) : a.size;
      const sizeB = b.isDir ? (dirSizeCache.get(b.id) ?? 0) : b.size;
      cmp = sizeA - sizeB;
    } else if (field === 'updatedAt') {
      cmp = new Date(a.updatedAt).getTime() - new Date(b.updatedAt).getTime();
    }

    if (cmp === 0) {
      cmp = collator.compare(a.name, b.name);
    }

    return order === 'asc' ? cmp : -cmp;
  });
}

/**
 * Recursively searches files within a volume (optionally scoped under a starting directory).
 */
export function searchFilesRecursively(
  allFiles: FileItem[],
  volumeId: string,
  query: string,
  startParentId: string | null = null
): FileItem[] {
  const trimmed = query.trim().toLowerCase();
  if (!trimmed) {
    return allFiles.filter((f) => f.volumeId === volumeId && f.parentId === startParentId);
  }

  const volumeFiles = allFiles.filter((f) => f.volumeId === volumeId);
  if (startParentId === null) {
    return volumeFiles.filter((f) => f.name.toLowerCase().includes(trimmed));
  }

  const descendantIds = collectRecursiveDeleteIds(volumeFiles, [startParentId]);
  descendantIds.delete(startParentId);
  return volumeFiles.filter(
    (f) => descendantIds.has(f.id) && f.name.toLowerCase().includes(trimmed)
  );
}

/**
 * Builds the breadcrumb trail from root to the given folder ID.
 */
export function buildBreadcrumbs(
  allFiles: FileItem[],
  volumeId: string,
  currentFolderId: string | null
): FileItem[] {
  const trail: FileItem[] = [];
  if (!currentFolderId) return trail;

  const byId = new Map<string, FileItem>();
  for (const item of allFiles) {
    if (item.volumeId === volumeId && item.isDir) {
      byId.set(item.id, item);
    }
  }

  let cursor: string | null = currentFolderId;
  const visited = new Set<string>();
  while (cursor && !visited.has(cursor)) {
    visited.add(cursor);
    const folder = byId.get(cursor);
    if (!folder) break;
    trail.unshift(folder);
    cursor = folder.parentId;
  }
  return trail;
}

/**
 * Builds human-readable full path for an item inside its volume.
 */
export function getItemFullPath(allFiles: FileItem[], item: FileItem): string {
  const crumbs = buildBreadcrumbs(allFiles, item.volumeId, item.parentId);
  const prefix = crumbs.map((c) => c.name).join('/');
  return prefix ? `/${prefix}/${item.name}` : `/${item.name}`;
}

/**
 * Splits 100 integer percent points across parts so the visible total is exactly 100.
 *
 * Input: byte counts (non-negative); all-zero input is allowed.
 * Output: integer percentages with the same length, summing to 100 (all zeros when empty).
 * Expected: largest remainder method — floor every exact share, then give the
 * leftover points to the biggest fractional parts. 99.6% therefore shows as 99%,
 * and the points are taken from the smallest shares instead of rounding up to 100%.
 */
export function apportionPercents(parts: number[]): number[] {
  const total = parts.reduce((acc, p) => acc + Math.max(0, p), 0);
  if (total <= 0) return parts.map(() => 0);
  const exact = parts.map((p) => (Math.max(0, p) / total) * 100);
  const base = exact.map((v) => Math.floor(v));
  let left = 100 - base.reduce((acc, v) => acc + v, 0);
  const order = exact
    .map((v, i) => ({ i, frac: v - Math.floor(v) }))
    .sort((a, b) => b.frac - a.frac || a.i - b.i);
  const out = base.slice();
  for (let k = 0; k < order.length && left > 0; k += 1) {
    out[order[k].i] += 1;
    left -= 1;
  }
  return out;
}

/**
 * Aggregates per-volume totals for the overview cards.
 */
export function computeOverviewSummary(volumes: VolumeInfo[], files: FileItem[]) {
  const perVolume = volumes.map((vol) => {
    const stats = computeDirectoryStats(files, vol.id, null);
    return {
      volume: vol,
      fileCount: stats.fileCount,
      folderCount: stats.folderCount,
      totalBytes: stats.totalBytes,
    };
  });

  const totalVolumes = volumes.length;
  const totalFiles = perVolume.reduce((acc, v) => acc + v.fileCount, 0);
  const totalBytes = perVolume.reduce((acc, v) => acc + v.totalBytes, 0);
  // 百分比用最大余数法分配，保证所有卷加起来正好 100
  const pcts = apportionPercents(perVolume.map((v) => v.totalBytes));

  return {
    totalVolumes,
    totalFiles,
    totalBytes,
    perVolume: perVolume.map((v, i) => ({ ...v, sharePct: pcts[i] })),
  };
}
