import React, { useEffect, useMemo, useRef, useState } from 'react';
import {
  AlertTriangle,
  ArrowDown,
  ArrowUp,
  ChevronLeft,
  ChevronRight,
  CornerLeftUp,
  Database,
  Download,
  Eye,
  FolderPlus,
  HardDrive,
  LayoutGrid,
  List,
  Pencil,
  Plus,
  RefreshCw,
  Search,
  Trash2,
  X,
} from 'lucide-react';
import { motion } from 'motion/react';
import type {
  ConnectionStatus,
  FileCategory,
  FileItem,
  Locale,
  ManageableChannel,
  SortField,
  SortOrder,
  VolumeInfo,
} from '../types';
import {
  buildBreadcrumbs,
  canMoveItemsToFolder,
  computeDirectoryStats,
  formatBytes,
  getItemFullPath,
  searchFilesRecursively,
  sortFileItems,
  volumeDisplayName,
} from '../utils/fsLogic';
import {
  formatDateTime,
  formatNumber,
  formatPlural,
  interpolate,
  type Translations,
} from '../utils/i18n';
import {
  matchesShortcut,
  matchesShortcutWithShift,
  SHORTCUT_ACTIONS,
  type ShortcutMap,
} from '../utils/shortcuts';
import { collectDroppedFiles } from '../utils/dropEntries';
import { sourcesFromFiles, type UploadSource } from '../utils/uploadPlan';
import { runViewTransition } from '../utils/viewTransition';
import { listChannels as apiListChannels } from '../api';
import { errText } from '../utils/errorText';
import { Button, Input, IOS_BOUNCE_SPRING, Select, useInterruptibleViewSpring } from './ui';
import { FileThumb, RichFileTypeEmblem } from './VisualAssets';
import { UploadMenu } from './UploadMenu';
import { FilesDialogs } from './FilesDialogs';
import { validateEntryName } from './filesHelpers';

interface FilesViewProps {
  t: Translations;
  locale: Locale;
  volumes: VolumeInfo[];
  activeVolumeId: string;
  onChangeVolume: (volId: string) => void;
  currentFolderId: string | null;
  onNavigateFolder: (folderId: string | null) => void;
  files: FileItem[];
  loading?: boolean;
  connectionStatus: ConnectionStatus;
  onUploadSources: (sources: UploadSource[], volumeId: string, parentId: string | null) => void;
  onImportFiles?: (volumeId: string, dir: string) => void;
  onImportFolder?: (volumeId: string, dir: string) => void;
  canPickPaths?: boolean;
  onNewTextFile: () => void;
  onPasteClipboard: () => void;
  onNotifyError: (msg: string) => void;
  onCreateFolder: (volumeId: string, parentId: string | null, name: string) => void;
  onRenameMoveItem: (itemId: string, newName: string, targetParentId: string | null) => void;
  onBatchMoveItems: (itemIds: string[], targetParentId: string | null) => void;
  onMoveItems: (itemIds: string[], targetParentId: string | null) => void;
  onDeleteItems: (itemIds: string[]) => void;
  onDownloadFile: (file: FileItem) => void;
  onPreviewFile: (file: FileItem) => void;
  canDownload: boolean;
  canPaste: boolean;
  canCreateVolume: boolean;
  canDeleteVolume: boolean;
  canAttachVolume: boolean;
  canListChannels: boolean;
  onCreateVolume: (name: string, title: string) => void;
  onAttachVolume: (name: string, chatId: number) => void;
  onDeleteVolume: (name: string) => void;
  onRefresh: () => Promise<void>;
  onRebuildVolume?: (volumeId: string) => Promise<void>;
  shortcuts: ShortcutMap;
}

/** 站内拖拽载荷 MIME：与「从系统拖文件进来上传」区分开 */
const DRAG_MIME = 'application/x-mytgfs-ids';
const PAGE_SIZE = 50;
const PAGE_SIZE_OPTIONS = [50, 100, 200] as const;
const VIEW_STORAGE = {
  mode: 'mytgfs_view_mode_v1',
  sortField: 'mytgfs_sort_field_v1',
  sortOrder: 'mytgfs_sort_order_v1',
  pageSize: 'mytgfs_page_size_v1',
} as const;

// 读取白名单内的视图偏好；存量的手改坏值不会把界面带进未定义状态。
function readStored<T extends string>(key: string, allowed: readonly T[], fallback: T): T {
  try {
    const raw = window.localStorage.getItem(key);
    return raw && (allowed as readonly string[]).includes(raw) ? (raw as T) : fallback;
  } catch {
    return fallback;
  }
}

function readStoredNumber(key: string, allowed: readonly number[], fallback: number): number {
  try {
    const n = Number(window.localStorage.getItem(key));
    return allowed.includes(n) ? n : fallback;
  } catch {
    return fallback;
  }
}

/** 可挂载频道：user 会话列出后由用户挑一个 */

// 按频道标题猜一个合法卷名；标题全是中文时回落 ch-<id>。
function suggestVolumeName(title: string, id: number): string {
  const cleaned = title
    .toLowerCase()
    .replace(/[^a-z0-9_-]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 32);
  return cleaned || `ch-${id}`;
}

export const FilesView: React.FC<FilesViewProps> = (p) => {
  const { t, locale, volumes, activeVolumeId, files, shortcuts } = p;
  const fileInputRef = useRef<HTMLInputElement | null>(null);
  const dirInputRef = useRef<HTMLInputElement | null>(null);
  const searchInputRef = useRef<HTMLInputElement | null>(null);
  const listRef = useRef<HTMLDivElement | null>(null);

  // ---- 视图偏好：模式/排序/每页条数，读 localStorage 并即时回写 ----
  const [viewMode, setViewMode] = useState<'list' | 'grid'>(() =>
    readStored(VIEW_STORAGE.mode, ['list', 'grid'] as const, 'list')
  );
  const [sortField, setSortField] = useState<SortField>(() =>
    readStored(VIEW_STORAGE.sortField, ['name', 'size', 'updatedAt'] as const, 'name')
  );
  const [sortOrder, setSortOrder] = useState<SortOrder>(() =>
    readStored(VIEW_STORAGE.sortOrder, ['asc', 'desc'] as const, 'asc')
  );
  const [pageSize, setPageSize] = useState(() =>
    readStoredNumber(VIEW_STORAGE.pageSize, PAGE_SIZE_OPTIONS, PAGE_SIZE)
  );
  useEffect(() => {
    try {
      localStorage.setItem(VIEW_STORAGE.mode, viewMode);
      localStorage.setItem(VIEW_STORAGE.sortField, sortField);
      localStorage.setItem(VIEW_STORAGE.sortOrder, sortOrder);
      localStorage.setItem(VIEW_STORAGE.pageSize, String(pageSize));
    } catch {
      // 隐私模式禁用存储时忽略：偏好只在本次会话生效
    }
  }, [viewMode, sortField, sortOrder, pageSize]);

  const [searchQuery, setSearchQuery] = useState('');
  const [searchTerm, setSearchTerm] = useState('');
  const [currentPage, setCurrentPage] = useState(1);
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set());
  const [cursorId, setCursorId] = useState<string | null>(null);
  const [dropTargetId, setDropTargetId] = useState<string | null>(null);

  // 弹窗状态：新建目录 / 改名移动 / 批量移动 / 删除确认
  const [newFolder, setNewFolder] = useState<string | null>(null);
  const [editingItem, setEditingItem] = useState<FileItem | null>(null);
  const [editName, setEditName] = useState('');
  const [editTarget, setEditTarget] = useState<string | null>(null);
  const [batchMove, setBatchMove] = useState<string | null>(null);
  const [deleteIds, setDeleteIds] = useState<string[] | null>(null);

  // 卷管理弹窗状态
  const [refreshing, setRefreshing] = useState(false);
  const [rebuilding, setRebuilding] = useState(false);
  const [showRebuild, setShowRebuild] = useState(false);
  const [showCreate, setShowCreate] = useState(false);
  const [newVolume, setNewVolume] = useState({ name: '', title: '' });
  const [volumeError, setVolumeError] = useState('');
  const [showAttach, setShowAttach] = useState(false);
  const [attachForm, setAttachForm] = useState({ name: '', chatId: '' });
  const [attachError, setAttachError] = useState('');
  const [channels, setChannels] = useState<ManageableChannel[]>([]);
  const [channelsState, setChannelsState] = useState<'idle' | 'loading' | 'ready' | 'error'>('idle');
  const [channelsError, setChannelsError] = useState('');
  const [showDelete, setShowDelete] = useState(false);
  const [deleteInput, setDeleteInput] = useState('');

  const currentVolume = volumes.find((v) => v.id === activeVolumeId) || volumes[0];

  // 搜索防抖：每次敲键都重排整个卷太浪费，200ms 静默后再过滤
  useEffect(() => {
    const timer = window.setTimeout(() => setSearchTerm(searchQuery), 200);
    return () => window.clearTimeout(timer);
  }, [searchQuery]);

  const breadcrumbs = useMemo(
    () => buildBreadcrumbs(files, activeVolumeId, p.currentFolderId),
    [files, activeVolumeId, p.currentFolderId]
  );
  const currentDirPath = useMemo(() => {
    if (!p.currentFolderId) return '/';
    const item = files.find((f) => f.id === p.currentFolderId);
    return item ? getItemFullPath(files, item) : '/';
  }, [files, p.currentFolderId]);

  const visibleItems = useMemo(
    () =>
      sortFileItems(
        searchFilesRecursively(files, activeVolumeId, searchTerm, p.currentFolderId),
        sortField,
        sortOrder,
        files,
        locale
      ),
    [files, activeVolumeId, searchTerm, p.currentFolderId, sortField, sortOrder, locale]
  );
  const totalPages = Math.max(1, Math.ceil(visibleItems.length / pageSize));
  const safePage = Math.min(currentPage, totalPages);
  const pageItems = useMemo(
    () => visibleItems.slice((safePage - 1) * pageSize, safePage * pageSize),
    [visibleItems, safePage, pageSize]
  );

  // 目录大小按当前页算一次并缓存：逐行调用 computeDirectoryStats 会退化成 O(行数 × 全量)
  const directorySizes = useMemo(() => {
    const out = new Map<string, number>();
    for (const item of pageItems) {
      if (item.isDir) out.set(item.id, computeDirectoryStats(files, item.volumeId, item.id).totalBytes);
    }
    return out;
  }, [pageItems, files]);

  const springStyle = useInterruptibleViewSpring(
    `${activeVolumeId}-${p.currentFolderId ?? 'root'}-${viewMode}-${safePage}`
  );

  const allPageSelected = pageItems.length > 0 && pageItems.every((i) => selectedIds.has(i.id));
  const somePageSelected = pageItems.some((i) => selectedIds.has(i.id));
  const clearSelection = () => {
    setSelectedIds(new Set());
    setCursorId(null);
  };
  const toggleSelectOne = (id: string) =>
    setSelectedIds((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  const toggleSelectPage = () => {
    const ids = pageItems.map((i) => i.id);
    setSelectedIds((prev) => {
      const next = new Set(prev);
      if (allPageSelected) ids.forEach((id) => next.delete(id));
      else ids.forEach((id) => next.add(id));
      return next;
    });
  };

  const openFolder = (folderId: string | null) =>
    runViewTransition(() => {
      p.onNavigateFolder(folderId);
      clearSelection();
      setCurrentPage(1);
      setSearchQuery('');
    });
  const upOneLevel = () => {
    if (!p.currentFolderId) return;
    openFolder(files.find((f) => f.id === p.currentFolderId)?.parentId ?? null);
  };

  // Esc 统一在 document 上监听：空目录里列表容器不渲染，挂在容器上就按不到
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.defaultPrevented || !matchesShortcut(e, shortcuts.back)) return;
      if (document.querySelector('dialog[open]')) return;
      const tag = (e.target as HTMLElement | null)?.tagName ?? '';
      if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT') {
        if (searchInputRef.current && e.target === searchInputRef.current) {
          e.preventDefault();
          setSearchQuery('');
          setCurrentPage(1);
          searchInputRef.current.blur();
          listRef.current?.focus();
        }
        return;
      }
      if (selectedIds.size > 0 || cursorId) {
        e.preventDefault();
        clearSelection();
        return;
      }
      if (!p.currentFolderId) return;
      e.preventDefault();
      upOneLevel();
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selectedIds, cursorId, p.currentFolderId, files, shortcuts]);

  // 键盘游标移出视口时把目标行带回来
  useEffect(() => {
    if (!cursorId) return;
    document.querySelector(`[data-file-id="${CSS.escape(cursorId)}"]`)?.scrollIntoView({ block: 'nearest' });
  }, [cursorId]);

  // 进入文件页把焦点交给列表：纯键盘用户切过来就能直接按方向键
  useEffect(() => {
    const el = listRef.current;
    if (!el) return;
    const active = document.activeElement as HTMLElement | null;
    if ((!active || active === document.body || !active.closest('#main')) && !document.querySelector('dialog[open]')) {
      el.focus({ preventScroll: true });
    }
  }, []);

  const onDropFiles = (e: React.DragEvent<HTMLDivElement>) => {
    if (e.dataTransfer.types.includes(DRAG_MIME)) return;
    e.preventDefault();
    void collectDroppedFiles(e.dataTransfer)
      .then((sources) => {
        if (sources.length > 0) p.onUploadSources(sources, activeVolumeId, p.currentFolderId);
      })
      .catch(() => p.onNotifyError(t.files.dropReadFailed));
  };
  const onDragStartItem = (e: React.DragEvent, item: FileItem) => {
    const ids = selectedIds.has(item.id) ? Array.from(selectedIds) : [item.id];
    e.dataTransfer.setData(DRAG_MIME, JSON.stringify(ids));
    e.dataTransfer.effectAllowed = 'move';
  };
  const onFolderDragOver = (e: React.DragEvent, folderId: string) => {
    if (!e.dataTransfer.types.includes(DRAG_MIME)) return;
    e.preventDefault();
    e.stopPropagation();
    e.dataTransfer.dropEffect = 'move';
    setDropTargetId(folderId);
  };
  const onFolderDrop = (e: React.DragEvent, folderId: string | null) => {
    if (!e.dataTransfer.types.includes(DRAG_MIME)) return;
    e.preventDefault();
    e.stopPropagation();
    setDropTargetId(null);
    let ids: string[] = [];
    try {
      ids = JSON.parse(e.dataTransfer.getData(DRAG_MIME)) as string[];
    } catch {
      return;
    }
    if (ids.length > 0 && (!folderId || canMoveItemsToFolder(files, ids, folderId))) {
      p.onMoveItems(ids, folderId);
    }
  };

  // 列表键盘操作：↑↓ 移动、全选、勾选、打开、改名、删除
  const onListKeyDown = (e: React.KeyboardEvent) => {
    if (pageItems.length === 0) return;
    if (matchesShortcut(e, shortcuts.selectAll)) {
      e.preventDefault();
      setSelectedIds(new Set(pageItems.map((i) => i.id)));
      return;
    }
    const down = matchesShortcutWithShift(e, shortcuts.moveDown);
    const up = matchesShortcutWithShift(e, shortcuts.moveUp);
    if (down || up) {
      const step = down ? 1 : -1;
      const at = pageItems.findIndex((i) => i.id === cursorId);
      // 首/末项继续外走时不拦截，让页面级方向键把焦点移出列表
      if (at >= 0 && ((down && at === pageItems.length - 1) || (up && at === 0))) return;
      e.preventDefault();
      const next = at < 0 ? (step > 0 ? 0 : pageItems.length - 1) : Math.min(pageItems.length - 1, Math.max(0, at + step));
      const target = pageItems[next];
      if (!target) return;
      setCursorId(target.id);
      if (e.shiftKey) setSelectedIds((prev) => new Set(prev).add(target.id));
      return;
    }
    const current = pageItems.find((i) => i.id === cursorId);
    if (!current) return;
    if (matchesShortcut(e, shortcuts.toggleSelect)) {
      e.preventDefault();
      toggleSelectOne(current.id);
    } else if (matchesShortcut(e, shortcuts.confirm)) {
      e.preventDefault();
      if (current.isDir) openFolder(current.id);
      else p.onPreviewFile(current);
    } else if (matchesShortcut(e, shortcuts.rename)) {
      e.preventDefault();
      openEdit(current);
    } else if (matchesShortcut(e, shortcuts.delete)) {
      e.preventDefault();
      setDeleteIds(selectedIds.size > 0 ? [...selectedIds] : [current.id]);
    }
  };

  const submitNewFolder = (e: React.FormEvent) => {
    e.preventDefault();
    const name = (newFolder ?? '').trim();
    if (!name) return;
    if (/[\\/]/.test(name) || name === '.' || name === '..') {
      p.onNotifyError(t.files.nameNoSeparator);
      return;
    }
    if (new TextEncoder().encode(name).length > 255) {
      p.onNotifyError(t.files.nameTooLong);
      return;
    }
    p.onCreateFolder(activeVolumeId, p.currentFolderId, name);
    setNewFolder(null);
  };
  const openEdit = (item: FileItem) => {
    setEditingItem(item);
    setEditName(item.name);
    setEditTarget(item.parentId);
  };
  const submitRename = (e: React.FormEvent) => {
    e.preventDefault();
    if (!editingItem) return;
    const name = editName.trim();
    if (validateEntryName(name, editingItem, editTarget, files, t)) return;
    p.onRenameMoveItem(editingItem.id, name, editTarget);
    setEditingItem(null);
  };
  const submitBatchMove = (e: React.FormEvent) => {
    e.preventDefault();
    const ids = Array.from(selectedIds);
    if (ids.length === 0) return;
    p.onBatchMoveItems(ids, batchMove);
    setBatchMove(null);
    clearSelection();
  };
  const confirmDelete = () => {
    if (!deleteIds || deleteIds.length === 0) return;
    p.onDeleteItems(deleteIds);
    setSelectedIds((prev) => {
      const next = new Set(prev);
      deleteIds.forEach((id) => next.delete(id));
      return next;
    });
    setDeleteIds(null);
  };

  // 频道列表按需加载：只有用户点「载入」才连 Telegram，失败原因内联显示
  const loadChannels = () => {
    setChannelsState('loading');
    setChannelsError('');
    apiListChannels()
      .then((res) => {
        setChannels(res.channels ?? []);
        setChannelsState('ready');
      })
      .catch((err) => {
        setChannelsError(errText(err));
        setChannelsState('error');
      });
  };
  const pickChannel = (ch: ManageableChannel) =>
    setAttachForm((v) => ({
      name: v.name || suggestVolumeName(ch.title, ch.id),
      chatId: String(ch.chat_id),
    }));

  const ariaKeyShortcuts = SHORTCUT_ACTIONS.map((a) => shortcuts[a].replace(/^Ctrl\+/, 'Control+')).join(' ');
  const categoryLabel = (cat: FileCategory, isDir: boolean) => (isDir ? t.files.dirBadge : t.categories[cat] || cat);
  const crumbClass = (active: boolean, dropping: boolean) =>
    `h-8 px-2.5 rounded-md text-xs font-mono truncate max-w-[160px] transition-colors cursor-pointer ${dropping ? 'drop-target ' : ''}${
      active
        ? 'font-semibold text-blue-600 dark:text-blue-400 bg-blue-500/10'
        : 'text-zinc-600 dark:text-zinc-400 hover:text-zinc-950 dark:hover:text-white'
    }`;
  const rowActions = (item: FileItem, withTitle: boolean) => {
    const title = (text: string) => (withTitle ? text : undefined);
    return (
      <>
        {!item.isDir && (
          <>
            <Button type="button" variant="ghost" size="icon" onClick={() => p.onPreviewFile(item)} aria-label={`${t.files.actionPreview} ${item.name}`} title={title(t.files.actionPreview)}>
              <Eye className="w-3.5 h-3.5" />
            </Button>
            {p.canDownload && (
              <Button type="button" variant="ghost" size="icon" onClick={() => p.onDownloadFile(item)} aria-label={`${t.files.actionDownload} ${item.name}`} title={title(t.files.actionDownload)}>
                <Download className="w-3.5 h-3.5" />
              </Button>
            )}
          </>
        )}
        <Button type="button" variant="ghost" size="icon" onClick={() => openEdit(item)} aria-label={`${t.files.actionRenameMove} ${item.name}`} title={title(t.files.actionRenameMove)}>
          <Pencil className="w-3.5 h-3.5" />
        </Button>
        <Button type="button" variant="ghost" size="icon" onClick={() => setDeleteIds([item.id])} aria-label={`${t.files.actionDelete} ${item.name}`} title={title(t.files.actionDelete)} className="hover:text-red-600 dark:hover:text-red-400">
          <Trash2 className="w-3.5 h-3.5" />
        </Button>
      </>
    );
  };
  const highlight = (text: string) => {
    const needle = searchTerm.trim().toLowerCase();
    if (!needle) return <>{text}</>;
    const lower = text.toLowerCase();
    const out: React.ReactNode[] = [];
    let from = 0;
    for (;;) {
      const at = lower.indexOf(needle, from);
      if (at < 0) {
        if (from < text.length) out.push(text.slice(from));
        break;
      }
      if (at > from) out.push(text.slice(from, at));
      out.push(
        <mark className="hit" key={at}>
          {text.slice(at, at + needle.length)}
        </mark>
      );
      from = at + needle.length;
    }
    return <>{out}</>;
  };
  const nameButton = (item: FileItem) => (
    <button
      type="button"
      dir="ltr"
      onClick={() => (item.isDir ? openFolder(item.id) : p.onPreviewFile(item))}
      className="text-start text-sm font-medium text-zinc-900 dark:text-zinc-100 hover:text-blue-600 dark:hover:text-blue-400 truncate block w-full cursor-pointer"
    >
      {highlight(item.name)}
    </button>
  );
  const categoryVisual = (item: FileItem, size: 'sm' | 'lg') =>
    item.category === 'image' || item.category === 'video' ? (
      <FileThumb name={item.name} category={item.category} thumbnailUrl={item.thumbnailUrl} size={size === 'lg' ? 'lg' : undefined} />
    ) : (
      <RichFileTypeEmblem category={item.category} size={size} />
    );

  return (
    <div
      className="stagger space-y-4"
      onDragOver={(e) => {
        // 只对外部文件放行：站内拖拽由行/面包屑自己处理
        if (e.dataTransfer.types.includes('Files')) e.preventDefault();
      }}
      onDrop={onDropFiles}
    >
      <input ref={fileInputRef} type="file" multiple onChange={onPickInput} className="hidden" aria-label={t.files.uploadFileBtn} />
      <input
        ref={dirInputRef}
        type="file"
        // @ts-expect-error webkitdirectory is supported for directory upload
        webkitdirectory=""
        directory=""
        multiple
        onChange={onPickInput}
        className="hidden"
        aria-label={t.files.uploadDirBtn}
      />

      {/* 卷操作条：能力位为 false 的按钮整块不渲染 */}
      <div className="flex flex-wrap items-center gap-2">
        <label htmlFor="volume-select-control" className="text-xs font-medium text-zinc-500 dark:text-zinc-400 shrink-0">
          {t.files.volumeSelectorLabel}
        </label>
        <Select
          id="volume-select-control"
          value={activeVolumeId}
          onChange={(e) => {
            p.onChangeVolume(e.target.value);
            clearSelection();
            setCurrentPage(1);
            setSearchQuery('');
          }}
          className="min-w-0 max-w-[55%] sm:max-w-none sm:w-auto font-semibold"
        >
          {volumes.map((v) => (
            <option key={v.id} value={v.id} className="bg-white dark:bg-zinc-900 text-zinc-900 dark:text-zinc-100">
              {volumeDisplayName(v.name, v.title)}
            </option>
          ))}
        </Select>
        <Button type="button" variant="outline" disabled={refreshing} onClick={onRefresh} aria-label={t.files.refreshBtn} title={t.files.refreshBtn}>
          <RefreshCw className={`w-3.5 h-3.5 ${refreshing ? 'animate-spin' : ''}`} />
          <span>{t.files.refreshBtn}</span>
        </Button>
        {p.onRebuildVolume && (
          <Button type="button" variant="outline" disabled={rebuilding || !activeVolumeId} onClick={() => setShowRebuild(true)} aria-label={t.files.rebuildBtn} title={t.files.rebuildBtn}>
            <Database className={`w-3.5 h-3.5 ${rebuilding ? 'animate-pulse' : ''}`} />
            <span>{t.files.rebuildBtn}</span>
          </Button>
        )}
        {p.canCreateVolume && (
          <Button type="button" variant="outline" onClick={() => { setNewVolume({ name: '', title: '' }); setVolumeError(''); setShowCreate(true); }}>
            <Plus className="w-3.5 h-3.5" />
            <span>{t.volume.createBtn}</span>
          </Button>
        )}
        {p.canDeleteVolume && currentVolume && (
          <Button type="button" variant="outline" onClick={() => setShowDelete(true)} className="border-red-300/80 text-red-600 dark:border-red-500/40 dark:text-red-400 hover:bg-red-500/10">
            <Trash2 className="w-3.5 h-3.5" />
            <span>{t.volume.deleteBtn}</span>
          </Button>
        )}
      </div>

      <div className="surface-card rounded-xl overflow-hidden">
        {/* 工具条：面包屑（本身是放置目标）+ 搜索 + 排序 + 上传 + 视图切换 + 分页条数 */}
        <div className="p-3 sm:px-4 flex flex-col lg:flex-row lg:items-center justify-between gap-3 border-b border-zinc-200/80 dark:border-zinc-800/80">
          <nav aria-label="Breadcrumb" className="flex flex-wrap items-center gap-1 min-w-0">
            {p.currentFolderId !== null && (
              <Button type="button" variant="secondary" size="sm" onClick={upOneLevel} className="me-1">
                <CornerLeftUp className="w-3.5 h-3.5" />
                <span>{t.files.upOneLevel}</span>
              </Button>
            )}
            <motion.button
              type="button"
              dir="ltr"
              whileTap={{ scale: 0.91 }}
              transition={IOS_BOUNCE_SPRING}
              onClick={() => openFolder(null)}
              onDragOver={(e) => onFolderDragOver(e, '__root__')}
              onDragLeave={() => setDropTargetId(null)}
              onDrop={(e) => onFolderDrop(e, null)}
              className={crumbClass(p.currentFolderId === null, dropTargetId === '__root__')}
            >
              /{currentVolume?.name || t.files.rootCrumb}
            </motion.button>
            {breadcrumbs.map((crumb) => (
              <React.Fragment key={crumb.id}>
                <span className="text-zinc-400 text-xs font-mono" aria-hidden="true">
                  /
                </span>
                <motion.button
                  type="button"
                  dir="ltr"
                  whileTap={{ scale: 0.91 }}
                  transition={IOS_BOUNCE_SPRING}
                  onClick={() => openFolder(crumb.id)}
                  onDragOver={(e) => onFolderDragOver(e, crumb.id)}
                  onDragLeave={() => setDropTargetId(null)}
                  onDrop={(e) => onFolderDrop(e, crumb.id)}
                  className={crumbClass(crumb.id === p.currentFolderId, dropTargetId === crumb.id)}
                >
                  {crumb.name}
                </motion.button>
              </React.Fragment>
            ))}
          </nav>

          <div className="flex flex-wrap items-center gap-2 min-w-0">
            <div className="relative flex-1 sm:w-56 min-w-[160px]">
              <label htmlFor="recursive-file-search" className="sr-only">
                {t.files.searchPlaceholder}
              </label>
              <Search className="w-3.5 h-3.5 text-zinc-400 absolute start-3 top-1/2 -translate-y-1/2 pointer-events-none" />
              <Input
                id="recursive-file-search"
                ref={searchInputRef}
                type="search"
                value={searchQuery}
                onChange={(e) => {
                  setSearchQuery(e.target.value);
                  setCurrentPage(1);
                }}
                placeholder={t.files.searchPlaceholder}
                className="ps-8 pe-8 text-xs h-8"
              />
              {searchQuery && (
                <button
                  type="button"
                  onClick={() => {
                    setSearchQuery('');
                    setCurrentPage(1);
                  }}
                  aria-label={t.files.clearSearch}
                  className="w-8 h-8 absolute end-0 top-1/2 -translate-y-1/2 inline-flex items-center justify-center text-zinc-400 hover:text-zinc-700 dark:hover:text-zinc-200 cursor-pointer"
                >
                  <X className="w-3.5 h-3.5" />
                </button>
              )}
            </div>

            <div className="flex flex-wrap items-center gap-1.5 min-w-0">
              <label htmlFor="sort-field-select" className="sr-only">
                {t.files.sortByLabel}
              </label>
              <Select id="sort-field-select" value={sortField} onChange={(e) => setSortField(e.target.value as SortField)} className="h-8 text-xs shrink-0">
                <option value="name" className="bg-white dark:bg-zinc-900">{t.files.sortName}</option>
                <option value="size" className="bg-white dark:bg-zinc-900">{t.files.sortSize}</option>
                <option value="updatedAt" className="bg-white dark:bg-zinc-900">{t.files.sortTime}</option>
              </Select>
              <Button type="button" variant="outline" size="icon" onClick={() => setSortOrder((o) => (o === 'asc' ? 'desc' : 'asc'))} aria-label={sortOrder === 'asc' ? t.files.sortAsc : t.files.sortDesc} title={sortOrder === 'asc' ? t.files.sortAsc : t.files.sortDesc}>
                {sortOrder === 'asc' ? <ArrowUp className="w-4 h-4" /> : <ArrowDown className="w-4 h-4" />}
              </Button>
              <div className="flex items-center gap-1.5">
                <UploadMenu
                  t={t}
                  onPickFile={() => (p.canPickPaths && p.onImportFiles ? p.onImportFiles(activeVolumeId, currentDirPath) : fileInputRef.current?.click())}
                  onPickFolder={() => (p.canPickPaths && p.onImportFolder ? p.onImportFolder(activeVolumeId, currentDirPath) : dirInputRef.current?.click())}
                  onNewText={p.onNewTextFile}
                  onPasteClipboard={p.onPasteClipboard}
                  canPaste={p.canPaste}
                />
                <Button type="button" variant="outline" size="icon" onClick={() => { setNewFolder(''); }} aria-label={t.files.newFolderBtn} title={t.files.newFolderBtn}>
                  <FolderPlus className="w-4 h-4" />
                </Button>
              </div>
              <label htmlFor="page-size-select" className="sr-only">
                {t.files.pageSize}
              </label>
              <Select id="page-size-select" value={String(pageSize)} onChange={(e) => { setPageSize(Number(e.target.value)); setCurrentPage(1); }} className="h-8 text-xs" title={t.files.pageSize}>
                {PAGE_SIZE_OPTIONS.map((n) => (
                  <option key={n} value={n} className="bg-white dark:bg-zinc-900">{formatNumber(n, locale)}</option>
                ))}
              </Select>
              <div className="inline-flex items-center p-0.5 rounded-lg bg-zinc-100 dark:bg-zinc-800/80">
                {(['list', 'grid'] as const).map((mode) => {
                  const active = viewMode === mode;
                  return (
                    <motion.button
                      key={mode}
                      type="button"
                      whileTap={{ scale: 0.86 }}
                      transition={IOS_BOUNCE_SPRING}
                      onClick={() => setViewMode(mode)}
                      aria-label={mode === 'list' ? t.a11y.listView : t.a11y.gridView}
                      title={mode === 'list' ? t.a11y.listView : t.a11y.gridView}
                      className={`w-7 h-7 rounded-md inline-flex items-center justify-center transition-colors cursor-pointer ${
                        active ? 'bg-white dark:bg-zinc-900 text-zinc-950 dark:text-white shadow-2xs' : 'text-zinc-500 hover:text-zinc-900 dark:hover:text-white'
                      }`}
                    >
                      {mode === 'list' ? <List className="w-3.5 h-3.5" /> : <LayoutGrid className="w-3.5 h-3.5" />}
                    </motion.button>
                  );
                })}
              </div>
            </div>
          </div>
        </div>

        {selectedIds.size > 0 && (
          <div className="flex flex-wrap items-center justify-between gap-3 px-4 py-2.5 bg-blue-500/10 border-b border-blue-500/20">
            <span className="text-xs font-medium text-blue-700 dark:text-blue-300 tabular-nums">
              {formatPlural(selectedIds.size, t.plurals.selectedItems, locale)}
            </span>
            <div className="flex flex-wrap items-center gap-2">
              <Button type="button" variant="outline" size="sm" onClick={() => setBatchMove(p.currentFolderId)}>{t.files.batchMoveBtn}</Button>
              <Button type="button" variant="destructive" size="sm" onClick={() => setDeleteIds(Array.from(selectedIds))}>{t.files.batchDeleteBtn}</Button>
              <Button type="button" variant="ghost" size="sm" onClick={clearSelection}>{t.files.clearSelectionBtn}</Button>
            </div>
          </div>
        )}

        {p.connectionStatus === 'invalid_credentials' ? (
          <div className="p-12 text-center space-y-2">
            <AlertTriangle className="w-7 h-7 text-red-500 mx-auto" />
            <h3 className="text-sm font-semibold text-zinc-900 dark:text-zinc-100">{t.files.connErrorEmptyTitle}</h3>
            <p className="text-xs text-zinc-500 dark:text-zinc-400 max-w-md mx-auto">{t.files.connErrorEmptyDesc}</p>
          </div>
        ) : p.loading && files.length === 0 ? (
          <div className="p-4 space-y-3" aria-busy="true" aria-live="polite">
            {Array.from({ length: 6 }, (_, i) => (
              <div key={i} className="flex items-center gap-3">
                <div className="skeleton w-9 h-9 rounded-lg shrink-0" />
                <div className="flex-1 space-y-2">
                  <div className="skeleton h-3 rounded w-1/3" />
                  <div className="skeleton h-2.5 rounded w-1/5" />
                </div>
                <div className="skeleton h-3 rounded w-16" />
              </div>
            ))}
          </div>
        ) : visibleItems.length === 0 ? (
          <div className="p-12 text-center space-y-2">
            <HardDrive className="w-8 h-8 text-zinc-400 mx-auto" />
            <h3 className="text-sm font-semibold text-zinc-900 dark:text-zinc-100">
              {searchTerm.trim() ? t.files.emptySearchTitle : t.files.emptyDirTitle}
            </h3>
          </div>
        ) : viewMode === 'grid' ? (
          <motion.div
            ref={listRef}
            style={springStyle}
            tabIndex={0}
            onKeyDown={onListKeyDown}
            aria-keyshortcuts={ariaKeyShortcuts}
            aria-label={t.files.colName}
            className="stagger p-4 grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4 will-change-transform outline-none focus-visible:ring-2 focus-visible:ring-blue-500/40 rounded-lg"
          >
            {pageItems.map((item) => {
              const isSelected = selectedIds.has(item.id);
              const displaySize = item.isDir ? (directorySizes.get(item.id) ?? 0) : item.size;
              return (
                <div
                  key={item.id}
                  data-file-id={item.id}
                  draggable
                  onDragStart={(e) => onDragStartItem(e, item)}
                  onDragOver={item.isDir ? (e) => onFolderDragOver(e, item.id) : undefined}
                  onDragLeave={item.isDir ? () => setDropTargetId(null) : undefined}
                  onDrop={item.isDir ? (e) => onFolderDrop(e, item.id) : undefined}
                  onMouseDown={() => setCursorId(item.id)}
                  className={`rounded-xl border border-zinc-200/80 dark:border-zinc-800/80 bg-zinc-50/40 dark:bg-zinc-900/40 overflow-hidden flex flex-col justify-between transition-all ${
                    isSelected ? 'ring-2 ring-blue-500' : ''
                  } ${item.isDir && dropTargetId === item.id ? 'drop-target' : ''} ${
                    cursorId === item.id ? 'ring-2 ring-[#007AFF]/70 dark:ring-[#0A84FF]/70' : ''
                  }`}
                >
                  <div>
                    <div className="relative h-32 bg-zinc-900/5 dark:bg-black/40 flex items-center justify-center overflow-hidden border-b border-zinc-200/60 dark:border-zinc-800/60">
                      {categoryVisual(item, 'lg')}
                      <label className="absolute top-2 start-2 w-8 h-8 inline-flex items-center justify-center cursor-pointer">
                        <input
                          type="checkbox"
                          aria-label={interpolate(t.a11y.selectItem, { name: item.name }, locale)}
                          checked={isSelected}
                          onChange={() => toggleSelectOne(item.id)}
                          className="w-4 h-4 rounded border-zinc-300 text-blue-600 focus:ring-blue-500 cursor-pointer"
                        />
                      </label>
                    </div>
                    <div className="p-3.5">
                      {nameButton(item)}
                      <div className="flex items-center gap-1.5 text-xs tabular-nums text-zinc-500 dark:text-zinc-400 mt-1">
                        <span>{categoryLabel(item.category, item.isDir)}</span>
                        <span aria-hidden="true">·</span>
                        <span dir="ltr">{formatBytes(displaySize, 2, locale)}</span>
                      </div>
                    </div>
                  </div>
                  <div className="px-3 pb-3 flex items-center justify-end gap-1">{rowActions(item, false)}</div>
                </div>
              );
            })}
          </motion.div>
        ) : (
          <motion.div
            ref={listRef}
            style={springStyle}
            tabIndex={0}
            onKeyDown={onListKeyDown}
            aria-keyshortcuts={ariaKeyShortcuts}
            aria-label={t.files.colName}
            className="will-change-transform outline-none focus-visible:ring-2 focus-visible:ring-blue-500/40 rounded-lg"
          >
            <div className="hidden md:grid md:grid-cols-12 items-center px-4 py-2.5 bg-zinc-50/80 dark:bg-zinc-900/50 border-b border-zinc-200/80 dark:border-zinc-800/80 text-xs font-medium text-zinc-500 dark:text-zinc-400">
              <div className="col-span-5 flex items-center gap-3">
                <label className="w-6 h-6 inline-flex items-center justify-center cursor-pointer">
                  <input
                    type="checkbox"
                    aria-label={t.a11y.selectAllPage}
                    checked={allPageSelected}
                    ref={(el) => {
                      // 半选态只能走 DOM 属性，React 没有对应受控 prop
                      if (el) el.indeterminate = somePageSelected && !allPageSelected;
                    }}
                    onChange={toggleSelectPage}
                    className="w-4 h-4 rounded border-zinc-300 text-blue-600 focus:ring-blue-500 cursor-pointer"
                  />
                </label>
                <span>{t.files.colName}</span>
              </div>
              <div className="col-span-2 text-end pe-4">{t.files.colSize}</div>
              <div className="col-span-2 text-end pe-4">{t.files.colUpdated}</div>
              <div className="col-span-3 text-end">{t.files.colActions}</div>
            </div>
            <div className="stagger divide-y divide-zinc-200/70 dark:divide-zinc-800/70">
              {pageItems.map((item) => {
                const isSelected = selectedIds.has(item.id);
                const displaySize = item.isDir ? (directorySizes.get(item.id) ?? 0) : item.size;
                return (
                  <div
                    key={item.id}
                    data-file-id={item.id}
                    draggable
                    onDragStart={(e) => onDragStartItem(e, item)}
                    onDragOver={item.isDir ? (e) => onFolderDragOver(e, item.id) : undefined}
                    onDragLeave={item.isDir ? () => setDropTargetId(null) : undefined}
                    onDrop={item.isDir ? (e) => onFolderDrop(e, item.id) : undefined}
                    onMouseDown={() => setCursorId(item.id)}
                    className={`p-3 md:px-4 md:py-2.5 flex flex-col md:grid md:grid-cols-12 md:items-center gap-2.5 transition-colors ${
                      isSelected ? 'bg-blue-500/[0.06] dark:bg-blue-500/[0.09]' : 'hover:bg-zinc-50/80 dark:hover:bg-white/[0.02]'
                    } ${item.isDir && dropTargetId === item.id ? 'drop-target' : ''} ${
                      cursorId === item.id ? 'ring-2 ring-inset ring-[#007AFF]/70 dark:ring-[#0A84FF]/70' : ''
                    } ${pageItems.length > 120 ? 'row-cv' : ''}`}
                  >
                    <div className="md:col-span-5 flex items-center gap-3 min-w-0">
                      <label className="w-6 h-6 inline-flex items-center justify-center shrink-0 cursor-pointer">
                        <input
                          type="checkbox"
                          aria-label={interpolate(t.a11y.selectItem, { name: item.name }, locale)}
                          checked={isSelected}
                          onChange={() => toggleSelectOne(item.id)}
                          className="w-4 h-4 rounded border-zinc-300 text-blue-600 focus:ring-blue-500 cursor-pointer"
                        />
                      </label>
                      {categoryVisual(item, 'sm')}
                      <div className="min-w-0 flex-1">
                        {nameButton(item)}
                        <div className="flex flex-wrap items-center gap-1.5 text-xs tabular-nums text-zinc-500 dark:text-zinc-400 mt-0.5">
                          <span>{categoryLabel(item.category, item.isDir)}</span>
                          {searchTerm.trim() && (
                            <>
                              <span aria-hidden="true">·</span>
                              <span dir="ltr" className="truncate max-w-[180px] font-mono">{getItemFullPath(files, item)}</span>
                            </>
                          )}
                          <span className="md:hidden" aria-hidden="true">·</span>
                          <span dir="ltr" className="md:hidden">{formatBytes(displaySize, 2, locale)}</span>
                          <span className="md:hidden" aria-hidden="true">·</span>
                          <span className="md:hidden">{formatDateTime(item.updatedAt, locale)}</span>
                        </div>
                      </div>
                    </div>
                    <div dir="ltr" className="hidden md:block md:col-span-2 text-end pe-4 font-mono tabular-nums text-xs text-zinc-700 dark:text-zinc-300">
                      {formatBytes(displaySize, 2, locale)}
                    </div>
                    <div className="hidden md:block md:col-span-2 text-end pe-4 font-mono tabular-nums text-xs text-zinc-500 dark:text-zinc-400">
                      {formatDateTime(item.updatedAt, locale)}
                    </div>
                    <div className="md:col-span-3 flex items-center justify-end gap-1">{rowActions(item, true)}</div>
                  </div>
                );
              })}
            </div>
          </motion.div>
        )}

        <div className="px-4 py-3 bg-zinc-50/70 dark:bg-zinc-900/40 border-t border-zinc-200/80 dark:border-zinc-800/80 flex flex-col sm:flex-row sm:items-center sm:justify-end gap-3">
          <div className="flex items-center justify-between sm:justify-end gap-3 shrink-0">
            <span className="text-xs tabular-nums text-zinc-500 dark:text-zinc-400">
              {interpolate(t.files.pageInfo, { page: safePage, total: totalPages, count: visibleItems.length }, locale)}
            </span>
            <div className="flex items-center gap-1.5">
              <Button type="button" variant="outline" size="sm" disabled={safePage <= 1} onClick={() => setCurrentPage((n) => Math.max(1, n - 1))}>
                <ChevronLeft className="w-3.5 h-3.5" />
                <span>{t.files.pagePrev}</span>
              </Button>
              <Button type="button" variant="outline" size="sm" disabled={safePage >= totalPages} onClick={() => setCurrentPage((n) => Math.min(totalPages, n + 1))}>
                <span>{t.files.pageNext}</span>
                <ChevronRight className="w-3.5 h-3.5" />
              </Button>
            </div>
          </div>
        </div>
      </div>

      <FilesDialogs
        t={t}
        locale={locale}
        files={files}
        volumeDirectories={files.filter((f) => f.volumeId === activeVolumeId && f.isDir)}
        currentVolume={currentVolume}
        activeVolumeId={activeVolumeId}
        showNewFolderModal={newFolder !== null}
        setShowNewFolderModal={(v: boolean) => setNewFolder(v ? newFolder ?? '' : null)}
        newFolderName={newFolder ?? ''}
        setNewFolderName={setNewFolder}
        submitNewFolder={submitNewFolder}
        editingItem={editingItem}
        setEditingItem={setEditingItem}
        editName={editName}
        setEditName={setEditName}
        editTargetFolderId={editTarget}
        setEditTargetFolderId={setEditTarget}
        submitRenameMove={submitRename}
        showBatchMoveModal={batchMove !== null}
        setShowBatchMoveModal={(v: boolean) => setBatchMove(v ? p.currentFolderId : null)}
        batchTargetFolderId={batchMove}
        setBatchTargetFolderId={setBatchMove}
        selectedIds={selectedIds}
        submitBatchMove={submitBatchMove}
        deleteConfirmIds={deleteIds}
        setDeleteConfirmIds={setDeleteIds}
        confirmDelete={confirmDelete}
        showCreateVolume={showCreate}
        canAttachVolume={p.canAttachVolume}
        setShowCreateVolume={setShowCreate}
        newVolume={newVolume}
        setNewVolume={setNewVolume}
        volumeFormError={volumeError}
        setVolumeFormError={setVolumeError}
        onCreateVolume={p.onCreateVolume}
        showAttachVolume={showAttach}
        setShowAttachVolume={setShowAttach}
        attachForm={attachForm}
        setAttachForm={setAttachForm}
        attachFormError={attachError}
        setAttachFormError={setAttachError}
        onAttachVolume={p.onAttachVolume}
        canListChannels={p.canListChannels}
        channels={channels}
        channelsState={channelsState}
        channelsError={channelsError}
        loadChannels={loadChannels}
        pickChannel={pickChannel}
        showDeleteVolume={showDelete}
        setShowDeleteVolume={setShowDelete}
        deleteVolumeInput={deleteInput}
        setDeleteVolumeInput={setDeleteInput}
        onDeleteVolume={p.onDeleteVolume}
        showRebuildConfirm={showRebuild}
        setShowRebuildConfirm={setShowRebuild}
        onRebuildVolume={p.onRebuildVolume}
        rebuilding={rebuilding}
        setRebuilding={setRebuilding}
        onNotifyError={p.onNotifyError}
      />
    </div>
  );

  async function onRefresh() {
    setRefreshing(true);
    try {
      await p.onRefresh();
    } finally {
      setRefreshing(false);
    }
  }

  function onPickInput(e: React.ChangeEvent<HTMLInputElement>) {
    if (e.target.files && e.target.files.length > 0) {
      // 目录选择靠 webkitRelativePath 保留层级；清空 value 以便重复选同一批文件
      p.onUploadSources(sourcesFromFiles(e.target.files), activeVolumeId, p.currentFolderId);
      e.target.value = '';
    }
  }
};
