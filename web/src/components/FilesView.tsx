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
  Link2,
  List,
  Pencil,
  Plus,
  RefreshCw,
  Search,
  Trash2,
  X,
} from 'lucide-react';
import {
  ConnectionStatus,
  FileCategory,
  FileItem,
  Locale,
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
  volumeDisplayName,
  sortFileItems,
} from '../utils/fsLogic';
import {
  matchesShortcut,
  matchesShortcutWithShift,
  SHORTCUT_ACTIONS,
  type ShortcutMap,
} from '../utils/shortcuts';
import { collectDroppedFiles } from '../utils/dropEntries';
import { sourcesFromFiles, type UploadSource } from '../utils/uploadPlan';
import { UploadMenu } from './UploadMenu';
import {
  Translations,
  formatDateTime,
  formatNumber,
  formatPlural,
  interpolate,
} from '../utils/i18n';
import { motion } from 'motion/react';
import { listChannels as apiListChannels } from '../api';
import { FileThumb, RichFileTypeEmblem } from './VisualAssets';
import { runViewTransition } from '../utils/viewTransition';
import { Button, IOS_BOUNCE_SPRING, Input, Select, useInterruptibleViewSpring } from './ui';
import { FilesDialogs } from './FilesDialogs';
import { validateEntryName } from './filesHelpers';

// Highlight 把搜索命中的片段包进 <mark>。
//
// 输入：text 原始文本；query 搜索词（大小写不敏感）
// 输出：React 片段
// 预期：空查询原样返回；多处命中全部高亮
function Highlight({ text, query }: { text: string; query: string }) {
  const needle = query.trim().toLowerCase();
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
}

interface FilesViewProps {
  t: Translations;
  locale: Locale;
  volumes: VolumeInfo[];
  activeVolumeId: string;
  onChangeVolume: (volId: string) => void;
  currentFolderId: string | null;
  onNavigateFolder: (folderId: string | null) => void;
  files: FileItem[];
  /** 首次加载中：目录为空时显示骨架而不是「空目录」 */
  loading?: boolean;
  connectionStatus: ConnectionStatus;
  /** 统一入口：文件/文件夹/拖拽/剪贴板/新建文本都进同一份上传清单 */
  onUploadSources: (sources: UploadSource[], volumeId: string, parentId: string | null) => void;
  /** 打开「新建文本」弹窗 */
  onNewTextFile: () => void;
  /** 读取系统剪贴板并上传（图片/文件）或预填文本 */
  onPasteClipboard: () => void;
  /** 非阻塞错误提示（拖拽读取失败等），由 App 转成 toast */
  onNotifyError: (msg: string) => void;
  onCreateFolder: (volumeId: string, parentId: string | null, name: string) => void;
  onRenameMoveItem: (itemId: string, newName: string, targetParentId: string | null) => void;
  onBatchMoveItems: (itemIds: string[], targetParentId: string | null) => void;
  /** 站内拖拽移动：拖到目录行或面包屑上时调用 */
  onMoveItems: (itemIds: string[], targetParentId: string | null) => void;
  onDeleteItems: (itemIds: string[]) => void;
  onDownloadFile: (file: FileItem) => void;
  onPreviewFile: (file: FileItem) => void;
  /** 设备能力：安卓没有对用户可见的下载目录，下载入口整块不渲染 */
  canDownload: boolean;
  /** 设备能力：安卓没有可读的剪贴板上传通道 */
  canPaste: boolean;
  /** 服务端能力：纯网页模式可能不允许建/删卷 */
  canCreateVolume: boolean;
  canDeleteVolume: boolean;
  canAttachVolume: boolean;
  canListChannels: boolean;
  onCreateVolume: (name: string, title: string) => void;
  onAttachVolume: (name: string, chatId: number) => void;
  onDeleteVolume: (name: string) => void;
  /** 手动刷新状态与列表；由 App 注入 */
  onRefresh: () => Promise<void>;
  /** 按频道历史重建索引；nil 表示服务端不支持 */
  onRebuildVolume?: (volumeId: string) => Promise<void>;
  /** 文件操作快捷键映射（设置页可改） */
  shortcuts: ShortcutMap;
}

// suggestVolumeName 按频道标题猜一个合法卷名。
//
// 输入：title 频道标题；id 频道 id
// 输出：只含 [A-Za-z0-9_-]、最长 32 的名字；标题全是中文时回落 ch-<id>
// 预期：只做建议，用户可以在表单里改
function suggestVolumeName(title: string, id: number): string {
  const cleaned = title
    .toLowerCase()
    .replace(/[^a-z0-9_-]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 32);
  return cleaned || `ch-${id}`;
}

const PAGE_SIZE = 50;

// 视图偏好在刷新后保留：排序、视图模式、每页条数都进 localStorage。
const VIEW_STORAGE = {
  mode: 'mytgfs_view_mode_v1',
  sortField: 'mytgfs_sort_field_v1',
  sortOrder: 'mytgfs_sort_order_v1',
  pageSize: 'mytgfs_page_size_v1',
};

// readStored 读取枚举型偏好；值不在白名单里就回落默认值。
function readStored<T extends string>(key: string, allowed: readonly T[], fallback: T): T {
  if (typeof window === 'undefined') return fallback;
  try {
    const raw = window.localStorage.getItem(key);
    return raw && (allowed as readonly string[]).includes(raw) ? (raw as T) : fallback;
  } catch {
    return fallback;
  }
}

// readStoredNumber 读取数字型偏好，同样做白名单校验。
function readStoredNumber(key: string, allowed: readonly number[], fallback: number): number {
  if (typeof window === 'undefined') return fallback;
  try {
    const n = Number(window.localStorage.getItem(key));
    return allowed.includes(n) ? n : fallback;
  } catch {
    return fallback;
  }
}

export const FilesView: React.FC<FilesViewProps> = ({
  t,
  locale,
  volumes,
  activeVolumeId,
  onChangeVolume,
  currentFolderId,
  onNavigateFolder,
  files,
  loading,
  connectionStatus,
  onUploadSources,
  onNewTextFile,
  onPasteClipboard,
  onNotifyError,
  onCreateFolder,
  onRenameMoveItem,
  onBatchMoveItems,
  onMoveItems,
  onDeleteItems,
  onDownloadFile,
  onPreviewFile,
  canDownload,
  canPaste,
  canCreateVolume,
  canDeleteVolume,
  canAttachVolume,
  canListChannels,
  onCreateVolume,
  onAttachVolume,
  onDeleteVolume,
  onRefresh,
  onRebuildVolume,
  shortcuts,
}) => {
  const fileInputRef = useRef<HTMLInputElement | null>(null);
  const dirInputRef = useRef<HTMLInputElement | null>(null);
  // 搜索框引用：Esc 离开搜索框后把焦点还给列表
  const searchInputRef = useRef<HTMLInputElement | null>(null);
  // 列表容器引用：Esc 离开搜索框后把焦点还给列表
  const listRef = useRef<HTMLDivElement | null>(null);
  const [refreshing, setRefreshing] = useState(false);
  const [rebuilding, setRebuilding] = useState(false);
  const [showRebuildConfirm, setShowRebuildConfirm] = useState(false);

  // 卷管理弹窗状态
  const [showCreateVolume, setShowCreateVolume] = useState(false);
  const [newVolume, setNewVolume] = useState({ name: '', title: '' });
  // 建卷/挂卷的行内错误：原生 alert 会阻塞整个窗口，改成字段下方红字
  const [volumeFormError, setVolumeFormError] = useState('');
  const [showAttachVolume, setShowAttachVolume] = useState(false);
  const [attachForm, setAttachForm] = useState({ name: '', chatId: '' });
  const [attachFormError, setAttachFormError] = useState('');
  // 频道列表按需加载：只有打开弹窗并点「载入」时才连 Telegram
  const [channels, setChannels] = useState<
    { id: number; chat_id: number; title: string; username: string; creator: boolean; admin: boolean }[]
  >([]);
  const [channelsState, setChannelsState] = useState<'idle' | 'loading' | 'ready' | 'error'>('idle');
  const [channelsError, setChannelsError] = useState('');

  // loadChannels 读取 user 账号可管理的频道。
  //
  // 输入：无；输出：更新 channels / channelsState
  // 预期：失败把原始原因内联显示，不弹窗打断
  const loadChannels = () => {
    setChannelsState('loading');
    setChannelsError('');
    apiListChannels()
      .then((res) => {
        setChannels(res.channels ?? []);
        setChannelsState('ready');
      })
      .catch((err) => {
        setChannelsError(err instanceof Error ? err.message : String(err));
        setChannelsState('error');
      });
  };

  // pickChannel 选中频道：填 chat_id，卷名为空时按标题给个建议名
  const pickChannel = (ch: { id: number; chat_id: number; title: string }) => {
    setAttachForm((v) => ({
      name: v.name || suggestVolumeName(ch.title, ch.id),
      chatId: String(ch.chat_id),
    }));
  };
  const [showDeleteVolume, setShowDeleteVolume] = useState(false);
  const [deleteVolumeInput, setDeleteVolumeInput] = useState('');

  const [viewMode, setViewMode] = useState<'list' | 'grid'>(() =>
    readStored(VIEW_STORAGE.mode, ['list', 'grid'] as const, 'list')
  );
  const [searchQuery, setSearchQuery] = useState('');
  // searchTerm 是防抖后的搜索词：输入框即时响应，过滤每 200ms 才跑一次
  const [searchTerm, setSearchTerm] = useState('');
  const [cursorId, setCursorId] = useState<string | null>(null);
  const [sortField, setSortField] = useState<SortField>(() =>
    readStored(VIEW_STORAGE.sortField, ['name', 'size', 'updatedAt'] as const, 'name')
  );
  const [sortOrder, setSortOrder] = useState<SortOrder>(() =>
    readStored(VIEW_STORAGE.sortOrder, ['asc', 'desc'] as const, 'asc')
  );
  const [pageSize, setPageSize] = useState(() =>
    readStoredNumber(VIEW_STORAGE.pageSize, [50, 100, 200], PAGE_SIZE)
  );
  const [currentPage, setCurrentPage] = useState(1);
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set());

  // Modals state
  const [showNewFolderModal, setShowNewFolderModal] = useState(false);
  const [newFolderName, setNewFolderName] = useState('');

  const [editingItem, setEditingItem] = useState<FileItem | null>(null);
  const [editName, setEditName] = useState('');
  const [editTargetFolderId, setEditTargetFolderId] = useState<string | null>(null);

  const [showBatchMoveModal, setShowBatchMoveModal] = useState(false);
  const [batchTargetFolderId, setBatchTargetFolderId] = useState<string | null>(null);

  const [deleteConfirmIds, setDeleteConfirmIds] = useState<string[] | null>(null);

  const currentVolume = volumes.find((v) => v.id === activeVolumeId) || volumes[0];

  const getCategoryLabel = (cat: FileCategory, isDir: boolean) => {
    if (isDir) return t.files.dirBadge;
    return t.categories[cat] || cat;
  };

  const breadcrumbs = useMemo(
    () => buildBreadcrumbs(files, activeVolumeId, currentFolderId),
    [files, activeVolumeId, currentFolderId]
  );

  // 搜索防抖：每次敲键都重排整个卷太浪费，200ms 静默后再过滤
  useEffect(() => {
    const timer = window.setTimeout(() => setSearchTerm(searchQuery), 200);
    return () => window.clearTimeout(timer);
  }, [searchQuery]);

  // 视图偏好落盘：下次打开保持同样的排序与视图
  useEffect(() => {
    try {
      window.localStorage.setItem(VIEW_STORAGE.mode, viewMode);
      window.localStorage.setItem(VIEW_STORAGE.sortField, sortField);
      window.localStorage.setItem(VIEW_STORAGE.sortOrder, sortOrder);
      window.localStorage.setItem(VIEW_STORAGE.pageSize, String(pageSize));
    } catch {
      // 隐私模式禁用存储时忽略：偏好只在本次会话生效
    }
  }, [viewMode, sortField, sortOrder, pageSize]);

  // Filtered and sorted items (directories always first, locale-aware Intl.Collator)
  const visibleItems = useMemo(() => {
    const raw = searchFilesRecursively(files, activeVolumeId, searchTerm, currentFolderId);
    return sortFileItems(raw, sortField, sortOrder, files, locale);
  }, [files, activeVolumeId, searchTerm, currentFolderId, sortField, sortOrder, locale]);

  const totalPages = Math.max(1, Math.ceil(visibleItems.length / pageSize));
  const safePage = Math.min(currentPage, totalPages);
  const paginatedItems = useMemo(() => {
    const start = (safePage - 1) * pageSize;
    return visibleItems.slice(start, start + pageSize);
  }, [visibleItems, safePage]);

  const fileListSpringStyle = useInterruptibleViewSpring(
    `${activeVolumeId}-${currentFolderId ?? 'root'}-${viewMode}-${safePage}`
  );

  const volumeDirectories = useMemo(
    () => files.filter((f) => f.volumeId === activeVolumeId && f.isDir),
    [files, activeVolumeId]
  );

  const handleVolumeSelect = (volId: string) => {
    onChangeVolume(volId);
    setSelectedIds(new Set());
    setCurrentPage(1);
    setSearchQuery('');
  };

  const handleFolderClick = (folderId: string | null) => {
    // 目录切换走 View Transition：列表整体过渡，不是硬闪
    runViewTransition(() => {
      onNavigateFolder(folderId);
      setSelectedIds(new Set());
      setCurrentPage(1);
      setSearchQuery('');
      setCursorId(null);
    });
  };

  const handleUpOneLevel = () => {
    if (!currentFolderId) return;
    const currentDir = files.find((f) => f.id === currentFolderId);
    handleFolderClick(currentDir?.parentId ?? null);
  };

  // Esc 统一在 document 上监听：
  // 空目录里列表容器不渲染，挂在容器上就按不到。
  //
  // 输入：键盘事件
  // 输出：无
  // 预期：先清选择，没有选择时返回上级目录；
  //       输入框/下拉里的同键（清搜索）与已打开的 dialog 不抢
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.defaultPrevented || !matchesShortcut(e, shortcuts.back)) return;
      if (document.querySelector('dialog[open]')) return;
      const tag = (e.target as HTMLElement | null)?.tagName ?? '';
      if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT') {
        // 搜索框里按取消键：清空搜索并把焦点还给列表，键盘操作可以接着走
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
        // 自己处理了 Esc，避免全局再兜底把焦点送去导航
        e.preventDefault();
        setSelectedIds(new Set());
        setCursorId(null);
        return;
      }
      if (!currentFolderId) return;
      e.preventDefault();
      handleUpOneLevel();
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
    // handleUpOneLevel 依赖 files/currentFolderId，直接把这些放进依赖里重新绑定
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selectedIds, cursorId, currentFolderId, files, shortcuts]);

  // 键盘移动游标时把目标行滚进视口，避免光标跑到屏幕外
  useEffect(() => {
    if (!cursorId) return;
    const el = document.querySelector(`[data-file-id="${CSS.escape(cursorId)}"]`);
    el?.scrollIntoView({ block: 'nearest' });
  }, [cursorId]);

  // 进入文件页时把焦点交给列表：纯键盘用户切过来就能直接按方向键/F2，
  // 不必先 Tab 一大圈。输入控件里或弹窗打开时不抢焦点。
  useEffect(() => {
    const el = listRef.current;
    if (!el) return;
    const active = document.activeElement as HTMLElement | null;
    const outsideMain = !active || active === document.body || !active.closest('#main');
    if (outsideMain && !document.querySelector('dialog[open]')) el.focus({ preventScroll: true });
  }, []);

  const toggleSelectOne = (id: string) => {
    setSelectedIds((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  const toggleSelectPage = () => {
    const pageIds = paginatedItems.map((i) => i.id);
    const allSelected = pageIds.length > 0 && pageIds.every((id) => selectedIds.has(id));
    setSelectedIds((prev) => {
      const next = new Set(prev);
      if (allSelected) {
        pageIds.forEach((id) => next.delete(id));
      } else {
        pageIds.forEach((id) => next.add(id));
      }
      return next;
    });
  };

  const handleFileInput = (e: React.ChangeEvent<HTMLInputElement>) => {
    if (e.target.files && e.target.files.length > 0) {
      // 目录选择靠 webkitRelativePath 保留层级；清空 value 以便重复选同一批文件
      onUploadSources(sourcesFromFiles(e.target.files), activeVolumeId, currentFolderId);
      e.target.value = '';
    }
  };

  // handleDrop 处理从系统拖进来的文件或文件夹。
  //
  // 输入：容器 drag/drop 事件
  // 输出：无
  // 预期：站内拖拽（带私有 MIME）交给文件夹移动逻辑，不当成上传；
  //       外部拖入的目录用 webkitGetAsEntry 递归展开后再进上传清单
  const handleDrop = (e: React.DragEvent<HTMLDivElement>) => {
    if (e.dataTransfer.types.includes(DRAG_MIME)) return;
    e.preventDefault();
    void collectDroppedFiles(e.dataTransfer)
      .then((sources) => {
        if (sources.length > 0) onUploadSources(sources, activeVolumeId, currentFolderId);
      })
      .catch(() => onNotifyError(t.files.dropReadFailed));
  };

  // ---- 站内拖拽移动 ----
  // 拖拽载荷用私有 MIME，避免和「从系统拖文件进来上传」混在一起。
  const DRAG_MIME = 'application/x-mytgfs-ids';
  const [dropTargetId, setDropTargetId] = useState<string | null>(null);

  // handleItemDragStart 记录要移动的条目：拖已选中的行就整批移动
  const handleItemDragStart = (e: React.DragEvent, item: FileItem) => {
    const ids = selectedIds.has(item.id) ? Array.from(selectedIds) : [item.id];
    e.dataTransfer.setData(DRAG_MIME, JSON.stringify(ids));
    e.dataTransfer.effectAllowed = 'move';
  };

  // handleFolderDragOver 只在站内拖拽时放行，外部文件仍走上传
  const handleFolderDragOver = (e: React.DragEvent, folderId: string) => {
    if (!e.dataTransfer.types.includes(DRAG_MIME)) return;
    e.preventDefault();
    e.stopPropagation();
    e.dataTransfer.dropEffect = 'move';
    setDropTargetId(folderId);
  };

  // handleFolderDrop 把拖来的条目移到目标目录；拖进自身子树直接忽略
  const handleFolderDrop = (e: React.DragEvent, folderId: string | null) => {
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
    if (ids.length === 0) return;
    if (folderId && !canMoveItemsToFolder(files, ids, folderId)) return;
    onMoveItems(ids, folderId);
  };

  // handleListKeyDown 提供列表键盘操作：↑↓ 移动、勾选、打开、改名、删除。
  //
  // 输入：键盘事件；焦点在列表容器上
  // 输出：无（改选中集/游标/打开弹窗）
  // 预期：键位来自设置页的 shortcuts；返回上级由文档级 Esc 统一处理
  const handleListKeyDown = (e: React.KeyboardEvent) => {
    const items = paginatedItems;
    if (items.length === 0) return;
    if (matchesShortcut(e, shortcuts.selectAll)) {
      e.preventDefault();
      setSelectedIds(new Set(items.map((i) => i.id)));
      return;
    }
    if (
      matchesShortcutWithShift(e, shortcuts.moveDown) ||
      matchesShortcutWithShift(e, shortcuts.moveUp)
    ) {
      const down = matchesShortcutWithShift(e, shortcuts.moveDown);
      const step = down ? 1 : -1;
      const at = items.findIndex((i) => i.id === cursorId);
      // 光标已经在首/末项还要继续往外走：不拦截按键，
      // 让页面级方向键把焦点移出列表（↑ 去工具栏搜索框、↓ 去分页）
      if (at >= 0 && ((down && at === items.length - 1) || (!down && at === 0))) return;
      e.preventDefault();
      const next = at < 0 ? (step > 0 ? 0 : items.length - 1) : Math.min(items.length - 1, Math.max(0, at + step));
      const target = items[next];
      if (!target) return;
      setCursorId(target.id);
      if (e.shiftKey) setSelectedIds((prev) => new Set(prev).add(target.id));
      return;
    }
    const current = items.find((i) => i.id === cursorId);
    if (!current) return;
    if (matchesShortcut(e, shortcuts.toggleSelect)) {
      e.preventDefault();
      toggleSelectOne(current.id);
      return;
    }
    if (matchesShortcut(e, shortcuts.confirm)) {
      e.preventDefault();
      if (current.isDir) handleFolderClick(current.id);
      else onPreviewFile(current);
      return;
    }
    if (matchesShortcut(e, shortcuts.rename)) {
      e.preventDefault();
      openEditModal(current);
      return;
    }
    if (matchesShortcut(e, shortcuts.delete)) {
      e.preventDefault();
      // 有勾选就删勾选的，没有勾选删游标所在项
      setDeleteConfirmIds(selectedIds.size > 0 ? [...selectedIds] : [current.id]);
    }
  };

  const submitNewFolder = (e: React.FormEvent) => {
    e.preventDefault();
    const trimmed = newFolderName.trim();
    if (!trimmed) return;
    onCreateFolder(activeVolumeId, currentFolderId, trimmed);
    setNewFolderName('');
    setShowNewFolderModal(false);
  };

  const openEditModal = (item: FileItem) => {
    setEditingItem(item);
    setEditName(item.name);
    setEditTargetFolderId(item.parentId);
  };

  const submitRenameMove = (e: React.FormEvent) => {
    e.preventDefault();
    if (!editingItem) return;
    const name = editName.trim();
    if (validateEntryName(name, editingItem, editTargetFolderId, files, t)) return;
    onRenameMoveItem(editingItem.id, name, editTargetFolderId);
    setEditingItem(null);
  };

  const submitBatchMove = (e: React.FormEvent) => {
    e.preventDefault();
    const ids = Array.from(selectedIds);
    if (ids.length === 0) return;
    onBatchMoveItems(ids, batchTargetFolderId);
    setShowBatchMoveModal(false);
    setSelectedIds(new Set());
  };

  const confirmDelete = () => {
    if (!deleteConfirmIds || deleteConfirmIds.length === 0) return;
    onDeleteItems(deleteConfirmIds);
    setSelectedIds((prev) => {
      const next = new Set(prev);
      deleteConfirmIds.forEach((id) => next.delete(id));
      return next;
    });
    setDeleteConfirmIds(null);
  };

  const renderCategoryVisual = (item: FileItem) => {
    if (item.category === 'image' || item.category === 'video') {
      return (
        <FileThumb name={item.name} category={item.category} thumbnailUrl={item.thumbnailUrl} />
      );
    }
    return <RichFileTypeEmblem category={item.category} size="sm" />;
  };

  // ariaKeyShortcuts 把用户键位转成 aria-keyshortcuts 语法。
  //
  // 输入：shortcuts 映射
  // 输出：空格分隔的键位串
  // 预期：Ctrl 写成 ARIA 的 Control；方向键也在可配置映射里
  const ariaKeyShortcuts = SHORTCUT_ACTIONS.map((action) =>
    shortcuts[action].replace(/^Ctrl\+/, 'Control+')
  ).join(' ');

  return (
    <div
      className="space-y-4"
      onDragOver={(e) => {
        e.preventDefault();
      }}
      onDrop={handleDrop}
    >
      {/* Hidden File & Directory Inputs */}
      <input
        ref={fileInputRef}
        type="file"
        multiple
        onChange={handleFileInput}
        className="hidden"
        aria-label={t.files.uploadFileBtn}
      />
      <input
        ref={dirInputRef}
        type="file"
        // @ts-expect-error webkitdirectory is supported for directory upload
        webkitdirectory=""
        directory=""
        multiple
        onChange={handleFileInput}
        className="hidden"
        aria-label={t.files.uploadDirBtn}
      />

      {/* Top Row: Volume Switcher + Volume Actions */}
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-3">
        <div className="flex items-center gap-2.5 min-w-0">
        <label
          htmlFor="volume-select-control"
          className="text-xs font-medium text-zinc-500 dark:text-zinc-400 shrink-0"
        >
          {t.files.volumeSelectorLabel}
        </label>
        <Select
          id="volume-select-control"
          value={activeVolumeId}
          onChange={(e) => handleVolumeSelect(e.target.value)}
          className="min-w-0 max-w-full sm:w-auto font-semibold"
        >
          {volumes.map((v) => (
            <option
              key={v.id}
              value={v.id}
              className="bg-white dark:bg-zinc-900 text-zinc-900 dark:text-zinc-100"
            >
              {volumeDisplayName(v.name, v.title)}
            </option>
          ))}
        </Select>
        </div>

        <div className="flex flex-wrap items-center gap-2">
          <Button
            type="button"
            variant="outline"
            disabled={refreshing}
            onClick={async () => {
              setRefreshing(true);
              try {
                await onRefresh();
              } finally {
                setRefreshing(false);
              }
            }}
            aria-label={t.files.refreshBtn}
            title={t.files.refreshBtn}
          >
            <RefreshCw className={`w-3.5 h-3.5 ${refreshing ? 'animate-spin' : ''}`} />
            <span>{t.files.refreshBtn}</span>
          </Button>
          {onRebuildVolume && (
            <Button
              type="button"
              variant="outline"
              disabled={rebuilding || !activeVolumeId}
              onClick={() => setShowRebuildConfirm(true)}
              aria-label={t.files.rebuildBtn}
              title={t.files.rebuildBtn}
            >
              <Database className={`w-3.5 h-3.5 ${rebuilding ? 'animate-pulse' : ''}`} />
              <span>{t.files.rebuildBtn}</span>
            </Button>
          )}
          {canCreateVolume && (
            <Button
              type="button"
              variant="outline"
              onClick={() => {
                setNewVolume({ name: '', title: '' });
                setVolumeFormError('');
                setShowCreateVolume(true);
              }}
            >
              <Plus className="w-3.5 h-3.5" />
              <span>{t.volume.createBtn}</span>
            </Button>
          )}

          {canAttachVolume && (
            <Button
              type="button"
              variant="outline"
              onClick={() => {
                setAttachForm({ name: '', chatId: '' });
                setAttachFormError('');
                setChannels([]);
                setChannelsState('idle');
                setChannelsError('');
                setShowAttachVolume(true);
              }}
            >
              <Link2 className="w-3.5 h-3.5" />
              <span>{t.volume.attachBtn}</span>
            </Button>
          )}

          {canDeleteVolume && currentVolume && (
            <Button
              type="button"
              variant="outline"
              onClick={() => setShowDeleteVolume(true)}
              className="border-red-300/80 text-red-600 dark:border-red-500/40 dark:text-red-400 hover:bg-red-500/10"
            >
              <Trash2 className="w-3.5 h-3.5" />
              <span>{t.volume.deleteBtn}</span>
            </Button>
          )}
        </div>
      </div>

      {/* Unified File Workbench Container */}
      <div className="surface-card rounded-xl overflow-hidden">
        {/* Workbench Command Bar: Breadcrumbs + Search + Sort + Grid/List Switcher */}
        <div className="p-3 sm:px-4 flex flex-col lg:flex-row lg:items-center justify-between gap-3 border-b border-zinc-200/80 dark:border-zinc-800/80">
          {/* Breadcrumb Path Navigation */}
          <nav aria-label="Breadcrumb" className="flex flex-wrap items-center gap-1 min-w-0">
            {currentFolderId !== null && (
              <Button
                type="button"
                variant="secondary"
                size="sm"
                onClick={handleUpOneLevel}
                className="me-1"
              >
                <CornerLeftUp className="w-3.5 h-3.5" />
                <span>{t.files.upOneLevel}</span>
              </Button>
            )}

            <motion.button
              type="button"
              dir="ltr"
              whileTap={{ scale: 0.91 }}
              transition={IOS_BOUNCE_SPRING}
              onClick={() => handleFolderClick(null)}
              onDragOver={(e) => handleFolderDragOver(e, '__root__')}
              onDragLeave={() => setDropTargetId(null)}
              onDrop={(e) => handleFolderDrop(e, null)}
              className={`h-8 px-2.5 rounded-md text-xs font-mono transition-colors cursor-pointer ${
                dropTargetId === '__root__' ? 'drop-target ' : ''
              }${
                currentFolderId === null
                  ? 'font-semibold text-blue-600 dark:text-blue-400 bg-blue-500/10'
                  : 'text-zinc-600 dark:text-zinc-400 hover:text-zinc-950 dark:hover:text-white'
              }`}
            >
              /{currentVolume?.name || t.files.rootCrumb}
            </motion.button>

            {breadcrumbs.map((crumb) => {
              const isCurrent = crumb.id === currentFolderId;
              return (
                <React.Fragment key={crumb.id}>
                  <span className="text-zinc-400 text-xs font-mono" aria-hidden="true">
                    /
                  </span>
                  <motion.button
                    type="button"
                    dir="ltr"
                    whileTap={{ scale: 0.91 }}
                    transition={IOS_BOUNCE_SPRING}
                    onClick={() => handleFolderClick(crumb.id)}
                    onDragOver={(e) => handleFolderDragOver(e, crumb.id)}
                    onDragLeave={() => setDropTargetId(null)}
                    onDrop={(e) => handleFolderDrop(e, crumb.id)}
                    className={`h-8 px-2.5 rounded-md text-xs font-mono truncate max-w-[160px] transition-colors cursor-pointer ${
                      dropTargetId === crumb.id ? 'drop-target ' : ''
                    }${
                      isCurrent
                        ? 'font-semibold text-blue-600 dark:text-blue-400 bg-blue-500/10'
                        : 'text-zinc-600 dark:text-zinc-400 hover:text-zinc-950 dark:hover:text-white'
                    }`}
                  >
                    {crumb.name}
                  </motion.button>
                </React.Fragment>
              );
            })}
          </nav>

          {/* Search & Sort & View Mode Controls */}
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

            <div className="flex items-center gap-1.5">
              <label htmlFor="sort-field-select" className="sr-only">
                {t.files.sortByLabel}
              </label>
              <Select
                id="sort-field-select"
                value={sortField}
                onChange={(e) => setSortField(e.target.value as SortField)}
                className="h-8 text-xs"
              >
                <option value="name" className="bg-white dark:bg-zinc-900">
                  {t.files.sortName}
                </option>
                <option value="size" className="bg-white dark:bg-zinc-900">
                  {t.files.sortSize}
                </option>
                <option value="updatedAt" className="bg-white dark:bg-zinc-900">
                  {t.files.sortTime}
                </option>
              </Select>

              <Button
                type="button"
                variant="outline"
                size="icon"
                onClick={() => setSortOrder((o) => (o === 'asc' ? 'desc' : 'asc'))}
                aria-label={sortOrder === 'asc' ? t.files.sortAsc : t.files.sortDesc}
                title={sortOrder === 'asc' ? t.files.sortAsc : t.files.sortDesc}
              >
                {sortOrder === 'asc' ? (
                  <ArrowUp className="w-4 h-4" />
                ) : (
                  <ArrowDown className="w-4 h-4" />
                )}
              </Button>

            <div className="flex items-center gap-1.5">
              <UploadMenu
                t={t}
                onPickFile={() => fileInputRef.current?.click()}
                onPickFolder={() => dirInputRef.current?.click()}
                onNewText={onNewTextFile}
                onPasteClipboard={onPasteClipboard}
                canPaste={canPaste}
              />
              <Button
                type="button"
                variant="outline"
                size="icon"
                onClick={() => setShowNewFolderModal(true)}
                aria-label={t.files.newFolderBtn}
                title={t.files.newFolderBtn}
              >
                <FolderPlus className="w-4 h-4" />
              </Button>
            </div>


              <label htmlFor="page-size-select" className="sr-only">
                {t.files.pageSize}
              </label>
              <Select
                id="page-size-select"
                value={String(pageSize)}
                onChange={(e) => {
                  setPageSize(Number(e.target.value));
                  setCurrentPage(1);
                }}
                className="h-8 text-xs"
                title={t.files.pageSize}
              >
                {[50, 100, 200].map((n) => (
                  <option key={n} value={n} className="bg-white dark:bg-zinc-900">
                    {formatNumber(n, locale)}
                  </option>
                ))}
              </Select>

              {/* View Mode Toggle: List vs Grid */}
              <div className="inline-flex items-center p-0.5 rounded-lg bg-zinc-100 dark:bg-zinc-800/80">
                <motion.button
                  type="button"
                  whileTap={{ scale: 0.86 }}
                  transition={IOS_BOUNCE_SPRING}
                  onClick={() => setViewMode('list')}
                  aria-label={t.a11y.listView}
                  title={t.a11y.listView}
                  className={`w-7 h-7 rounded-md inline-flex items-center justify-center transition-colors cursor-pointer ${
                    viewMode === 'list'
                      ? 'bg-white dark:bg-zinc-900 text-zinc-950 dark:text-white shadow-2xs'
                      : 'text-zinc-500 hover:text-zinc-900 dark:hover:text-white'
                  }`}
                >
                  <List className="w-3.5 h-3.5" />
                </motion.button>
                <motion.button
                  type="button"
                  whileTap={{ scale: 0.86 }}
                  transition={IOS_BOUNCE_SPRING}
                  onClick={() => setViewMode('grid')}
                  aria-label={t.a11y.gridView}
                  title={t.a11y.gridView}
                  className={`w-7 h-7 rounded-md inline-flex items-center justify-center transition-colors cursor-pointer ${
                    viewMode === 'grid'
                      ? 'bg-white dark:bg-zinc-900 text-zinc-950 dark:text-white shadow-2xs'
                      : 'text-zinc-500 hover:text-zinc-900 dark:hover:text-white'
                  }`}
                >
                  <LayoutGrid className="w-3.5 h-3.5" />
                </motion.button>
              </div>
            </div>
          </div>
        </div>

        {/* Multi-Select Batch Action Toolbar */}
        {selectedIds.size > 0 && (
          <div className="flex flex-wrap items-center justify-between gap-3 px-4 py-2.5 bg-blue-500/10 border-b border-blue-500/20">
            <span className="text-xs font-medium text-blue-700 dark:text-blue-300 tabular-nums">
              {formatPlural(selectedIds.size, t.plurals.selectedItems, locale)}
            </span>
            <div className="flex flex-wrap items-center gap-2">
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={() => {
                  setBatchTargetFolderId(currentFolderId);
                  setShowBatchMoveModal(true);
                }}
              >
                {t.files.batchMoveBtn}
              </Button>
              <Button
                type="button"
                variant="destructive"
                size="sm"
                onClick={() => setDeleteConfirmIds(Array.from(selectedIds))}
              >
                {t.files.batchDeleteBtn}
              </Button>
              <Button
                type="button"
                variant="ghost"
                size="sm"
                onClick={() => setSelectedIds(new Set())}
              >
                {t.files.clearSelectionBtn}
              </Button>
            </div>
          </div>
        )}

        {/* Table / Grid Content or Explicit Empty States */}
        {connectionStatus === 'invalid_credentials' ? (
          <div className="p-12 text-center space-y-2">
            <AlertTriangle className="w-7 h-7 text-red-500 mx-auto" />
            <h3 className="text-sm font-semibold text-zinc-900 dark:text-zinc-100">
              {t.files.connErrorEmptyTitle}
            </h3>
            <p className="text-xs text-zinc-500 dark:text-zinc-400 max-w-md mx-auto">
              {t.files.connErrorEmptyDesc}
            </p>
          </div>
        ) : loading && files.length === 0 ? (
          /* 骨架屏：首次加载还没拿到任何条目时占住版式，避免空白闪一下 */
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
            {searchTerm.trim() && (
              <p className="text-xs text-zinc-500 dark:text-zinc-400 max-w-md mx-auto">
                {t.files.emptySearchDesc}
              </p>
            )}
          </div>
        ) : viewMode === 'grid' ? (
          /* GALLERY GRID VIEW */
          <motion.div
            ref={listRef}
            style={fileListSpringStyle}
            tabIndex={0}
            onKeyDown={handleListKeyDown}
            aria-keyshortcuts={ariaKeyShortcuts}
            aria-label={t.files.colName}
            className="p-4 grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4 will-change-transform outline-none focus-visible:ring-2 focus-visible:ring-blue-500/40 rounded-lg"
          >
            {paginatedItems.map((item) => {
              const isSelected = selectedIds.has(item.id);
              const displaySize = item.isDir
                ? computeDirectoryStats(files, item.volumeId, item.id).totalBytes
                : item.size;

              return (
                <div
                  key={item.id}
                  data-file-id={item.id}
                  draggable
                  onDragStart={(e) => handleItemDragStart(e, item)}
                  onDragOver={item.isDir ? (e) => handleFolderDragOver(e, item.id) : undefined}
                  onDragLeave={item.isDir ? () => setDropTargetId(null) : undefined}
                  onDrop={item.isDir ? (e) => handleFolderDrop(e, item.id) : undefined}
                  onMouseDown={() => setCursorId(item.id)}
                  className={`rounded-xl border border-zinc-200/80 dark:border-zinc-800/80 bg-zinc-50/40 dark:bg-zinc-900/40 overflow-hidden flex flex-col justify-between transition-all ${
                    isSelected ? 'ring-2 ring-blue-500' : ''
                  } ${item.isDir && dropTargetId === item.id ? 'drop-target' : ''} ${
                    cursorId === item.id
                      ? 'ring-2 ring-[#007AFF]/70 dark:ring-[#0A84FF]/70'
                      : ''
                  }`}
                >
                  <div>
                    <div className="relative h-32 bg-zinc-900/5 dark:bg-black/40 flex items-center justify-center overflow-hidden border-b border-zinc-200/60 dark:border-zinc-800/60">
                      {item.category === 'image' || item.category === 'video' ? (
                        <FileThumb
                          name={item.name}
                          category={item.category}
                          thumbnailUrl={item.thumbnailUrl}
                          size="lg"
                        />
                      ) : (
                        <RichFileTypeEmblem category={item.category} size="lg" />
                      )}
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
                      <button
                        type="button"
                        dir="ltr"
                        onClick={() =>
                          item.isDir ? handleFolderClick(item.id) : onPreviewFile(item)
                        }
                        className="text-start text-sm font-medium text-zinc-950 dark:text-white hover:text-blue-600 dark:hover:text-blue-400 truncate block w-full cursor-pointer"
                      >
                        <Highlight text={item.name} query={searchTerm} />
                      </button>
                      <div className="flex items-center gap-1.5 text-xs tabular-nums text-zinc-500 dark:text-zinc-400 mt-1">
                        <span>{getCategoryLabel(item.category, item.isDir)}</span>
                        <span aria-hidden="true">·</span>
                        <span dir="ltr">{formatBytes(displaySize, 2, locale)}</span>
                      </div>
                    </div>
                  </div>

                  <div className="px-3 pb-3 flex items-center justify-end gap-1">
                    {!item.isDir && (
                      <>
                        <Button
                          type="button"
                          variant="ghost"
                          size="icon"
                          onClick={() => onPreviewFile(item)}
                          aria-label={`${t.files.actionPreview} ${item.name}`}
                        >
                          <Eye className="w-3.5 h-3.5" />
                        </Button>
                        {canDownload && (
                          <Button
                            type="button"
                            variant="ghost"
                            size="icon"
                            onClick={() => onDownloadFile(item)}
                            aria-label={`${t.files.actionDownload} ${item.name}`}
                          >
                            <Download className="w-3.5 h-3.5" />
                          </Button>
                        )}
                      </>
                    )}
                    <Button
                      type="button"
                      variant="ghost"
                      size="icon"
                      onClick={() => openEditModal(item)}
                      aria-label={`${t.files.actionRenameMove} ${item.name}`}
                    >
                      <Pencil className="w-3.5 h-3.5" />
                    </Button>
                    <Button
                      type="button"
                      variant="ghost"
                      size="icon"
                      onClick={() => setDeleteConfirmIds([item.id])}
                      aria-label={`${t.files.actionDelete} ${item.name}`}
                      className="hover:text-red-600 dark:hover:text-red-400"
                    >
                      <Trash2 className="w-3.5 h-3.5" />
                    </Button>
                  </div>
                </div>
              );
            })}
          </motion.div>
        ) : (
          /* LIST VIEW */
          <motion.div
            ref={listRef}
            style={fileListSpringStyle}
            tabIndex={0}
            onKeyDown={handleListKeyDown}
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
                    checked={
                      paginatedItems.length > 0 &&
                      paginatedItems.every((item) => selectedIds.has(item.id))
                    }
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

            <div className="divide-y divide-zinc-200/70 dark:divide-zinc-800/70">
              {paginatedItems.map((item) => {
                const isSelected = selectedIds.has(item.id);
                const displaySize = item.isDir
                  ? computeDirectoryStats(files, item.volumeId, item.id).totalBytes
                  : item.size;

                return (
                  <div
                    key={item.id}
                    data-file-id={item.id}
                    draggable
                    onDragStart={(e) => handleItemDragStart(e, item)}
                    onDragOver={item.isDir ? (e) => handleFolderDragOver(e, item.id) : undefined}
                    onDragLeave={item.isDir ? () => setDropTargetId(null) : undefined}
                    onDrop={item.isDir ? (e) => handleFolderDrop(e, item.id) : undefined}
                    onMouseDown={() => setCursorId(item.id)}
                    className={`p-3 md:px-4 md:py-2.5 flex flex-col md:grid md:grid-cols-12 md:items-center gap-2.5 transition-colors ${
                      isSelected
                        ? 'bg-blue-500/[0.06] dark:bg-blue-500/[0.09]'
                        : 'hover:bg-zinc-50/80 dark:hover:bg-white/[0.02]'
                    } ${item.isDir && dropTargetId === item.id ? 'drop-target' : ''} ${
                      cursorId === item.id
                        ? 'ring-2 ring-inset ring-[#007AFF]/70 dark:ring-[#0A84FF]/70'
                        : ''
                    } ${paginatedItems.length > 120 ? 'row-cv' : ''}`}
                  >
                    {/* Name & Checkbox */}
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

                      {renderCategoryVisual(item)}

                      <div className="min-w-0 flex-1">
                        <button
                          type="button"
                          dir="ltr"
                          onClick={() =>
                            item.isDir ? handleFolderClick(item.id) : onPreviewFile(item)
                          }
                          className="text-start text-sm font-medium text-zinc-900 dark:text-zinc-100 hover:text-blue-600 dark:hover:text-blue-400 truncate block w-full cursor-pointer"
                        >
                          <Highlight text={item.name} query={searchTerm} />
                        </button>

                        <div className="flex flex-wrap items-center gap-1.5 text-xs tabular-nums text-zinc-500 dark:text-zinc-400 mt-0.5">
                          <span>{getCategoryLabel(item.category, item.isDir)}</span>
                          {searchTerm.trim() && (
                            <>
                              <span aria-hidden="true">·</span>
                              <span dir="ltr" className="truncate max-w-[180px] font-mono">
                                {getItemFullPath(files, item)}
                              </span>
                            </>
                          )}
                          <span className="md:hidden" aria-hidden="true">·</span>
                          <span dir="ltr" className="md:hidden">
                            {formatBytes(displaySize, 2, locale)}
                          </span>
                          <span className="md:hidden" aria-hidden="true">·</span>
                          <span className="md:hidden">
                            {formatDateTime(item.updatedAt, locale)}
                          </span>
                        </div>
                      </div>
                    </div>

                    {/* Size Column */}
                    <div
                      dir="ltr"
                      className="hidden md:block md:col-span-2 text-end pe-4 font-mono tabular-nums text-xs text-zinc-700 dark:text-zinc-300"
                    >
                      {formatBytes(displaySize, 2, locale)}
                    </div>

                    {/* Updated Time Column */}
                    <div className="hidden md:block md:col-span-2 text-end pe-4 font-mono tabular-nums text-xs text-zinc-500 dark:text-zinc-400">
                      {formatDateTime(item.updatedAt, locale)}
                    </div>

                    {/* Actions Column */}
                    <div className="md:col-span-3 flex items-center justify-end gap-1">
                      {!item.isDir && (
                        <>
                          <Button
                            type="button"
                            variant="ghost"
                            size="icon"
                            onClick={() => onPreviewFile(item)}
                            aria-label={`${t.files.actionPreview} ${item.name}`}
                            title={t.files.actionPreview}
                          >
                            <Eye className="w-3.5 h-3.5" />
                          </Button>
                          {canDownload && (
                            <Button
                              type="button"
                              variant="ghost"
                              size="icon"
                              onClick={() => onDownloadFile(item)}
                              aria-label={`${t.files.actionDownload} ${item.name}`}
                              title={t.files.actionDownload}
                            >
                              <Download className="w-3.5 h-3.5" />
                            </Button>
                          )}
                        </>
                      )}
                      <Button
                        type="button"
                        variant="ghost"
                        size="icon"
                        onClick={() => openEditModal(item)}
                        aria-label={`${t.files.actionRenameMove} ${item.name}`}
                        title={t.files.actionRenameMove}
                      >
                        <Pencil className="w-3.5 h-3.5" />
                      </Button>
                      <Button
                        type="button"
                        variant="ghost"
                        size="icon"
                        onClick={() => setDeleteConfirmIds([item.id])}
                        aria-label={`${t.files.actionDelete} ${item.name}`}
                        title={t.files.actionDelete}
                        className="hover:text-red-600 dark:hover:text-red-400"
                      >
                        <Trash2 className="w-3.5 h-3.5" />
                      </Button>
                    </div>
                  </div>
                );
              })}
            </div>
          </motion.div>
        )}

        {/* Pagination Footer */}
        <div className="px-4 py-3 bg-zinc-50/70 dark:bg-zinc-900/40 border-t border-zinc-200/80 dark:border-zinc-800/80 flex flex-col sm:flex-row sm:items-center sm:justify-end gap-3">
          <div className="flex items-center justify-between sm:justify-end gap-3 shrink-0">
            <span className="text-xs tabular-nums text-zinc-500 dark:text-zinc-400">
              {interpolate(
                t.files.pageInfo,
                {
                  page: safePage,
                  total: totalPages,
                  count: visibleItems.length,
                },
                locale
              )}
            </span>
            <div className="flex items-center gap-1.5">
              <Button
                type="button"
                variant="outline"
                size="sm"
                disabled={safePage <= 1}
                onClick={() => setCurrentPage((p) => Math.max(1, p - 1))}
              >
                <ChevronLeft className="w-3.5 h-3.5" />
                <span>{t.files.pagePrev}</span>
              </Button>
              <Button
                type="button"
                variant="outline"
                size="sm"
                disabled={safePage >= totalPages}
                onClick={() => setCurrentPage((p) => Math.min(totalPages, p + 1))}
              >
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
        volumeDirectories={volumeDirectories}
        currentVolume={currentVolume}
        activeVolumeId={activeVolumeId}
        showNewFolderModal={showNewFolderModal}
        setShowNewFolderModal={setShowNewFolderModal}
        newFolderName={newFolderName}
        setNewFolderName={setNewFolderName}
        submitNewFolder={submitNewFolder}
        editingItem={editingItem}
        setEditingItem={setEditingItem}
        editName={editName}
        setEditName={setEditName}
        editTargetFolderId={editTargetFolderId}
        setEditTargetFolderId={setEditTargetFolderId}
        submitRenameMove={submitRenameMove}
        showBatchMoveModal={showBatchMoveModal}
        setShowBatchMoveModal={setShowBatchMoveModal}
        batchTargetFolderId={batchTargetFolderId}
        setBatchTargetFolderId={setBatchTargetFolderId}
        selectedIds={selectedIds}
        submitBatchMove={submitBatchMove}
        deleteConfirmIds={deleteConfirmIds}
        setDeleteConfirmIds={setDeleteConfirmIds}
        confirmDelete={confirmDelete}
        showCreateVolume={showCreateVolume}
        setShowCreateVolume={setShowCreateVolume}
        newVolume={newVolume}
        setNewVolume={setNewVolume}
        volumeFormError={volumeFormError}
        setVolumeFormError={setVolumeFormError}
        onCreateVolume={onCreateVolume}
        showAttachVolume={showAttachVolume}
        setShowAttachVolume={setShowAttachVolume}
        attachForm={attachForm}
        setAttachForm={setAttachForm}
        attachFormError={attachFormError}
        setAttachFormError={setAttachFormError}
        onAttachVolume={onAttachVolume}
        canListChannels={canListChannels}
        channels={channels}
        channelsState={channelsState}
        channelsError={channelsError}
        loadChannels={loadChannels}
        pickChannel={pickChannel}
        showDeleteVolume={showDeleteVolume}
        setShowDeleteVolume={setShowDeleteVolume}
        deleteVolumeInput={deleteVolumeInput}
        setDeleteVolumeInput={setDeleteVolumeInput}
        onDeleteVolume={onDeleteVolume}
        showRebuildConfirm={showRebuildConfirm}
        setShowRebuildConfirm={setShowRebuildConfirm}
        onRebuildVolume={onRebuildVolume}
        rebuilding={rebuilding}
        setRebuilding={setRebuilding}
      />
    </div>
  );
};
