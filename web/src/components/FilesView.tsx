import React, { useMemo, useRef, useState } from 'react';
import {
  AlertTriangle,
  ArrowDown,
  ArrowUp,
  ChevronLeft,
  ChevronRight,
  CornerLeftUp,
  Download,
  Eye,
  FolderPlus,
  FolderUp,
  HardDrive,
  LayoutGrid,
  List,
  Loader2,
  Pencil,
  Search,
  Trash2,
  Upload,
  X,
} from 'lucide-react';
import {
  ConnectionStatus,
  FileCategory,
  FileItem,
  IndexStatus,
  Locale,
  OfflineSavedFile,
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
} from '../utils/fsLogic';
import {
  Translations,
  formatDateTime,
  formatPercent,
  formatPlural,
  interpolate,
} from '../utils/i18n';
import { motion } from 'motion/react';
import { RichFileTypeEmblem } from './VisualAssets';
import { Button, IOS_BOUNCE_SPRING, Input, Select, useInterruptibleViewSpring } from './ui';

interface FilesViewProps {
  t: Translations;
  locale: Locale;
  volumes: VolumeInfo[];
  activeVolumeId: string;
  onChangeVolume: (volId: string) => void;
  currentFolderId: string | null;
  onNavigateFolder: (folderId: string | null) => void;
  files: FileItem[];
  offlineFiles: OfflineSavedFile[];
  connectionStatus: ConnectionStatus;
  onUploadFiles: (fileList: FileList, volumeId: string, parentId: string | null) => void;
  onCreateFolder: (volumeId: string, parentId: string | null, name: string) => void;
  onRenameMoveItem: (itemId: string, newName: string, targetParentId: string | null) => void;
  onBatchMoveItems: (itemIds: string[], targetParentId: string | null) => void;
  onDeleteItems: (itemIds: string[]) => void;
  onDownloadFile: (file: FileItem) => void;
  onSaveOffline: (file: FileItem) => void;
  onPreviewFile: (file: FileItem) => void;
}

const PAGE_SIZE = 50;

export const FilesView: React.FC<FilesViewProps> = ({
  t,
  locale,
  volumes,
  activeVolumeId,
  onChangeVolume,
  currentFolderId,
  onNavigateFolder,
  files,
  offlineFiles,
  connectionStatus,
  onUploadFiles,
  onCreateFolder,
  onRenameMoveItem,
  onBatchMoveItems,
  onDeleteItems,
  onDownloadFile,
  onSaveOffline,
  onPreviewFile,
}) => {
  const fileInputRef = useRef<HTMLInputElement | null>(null);
  const dirInputRef = useRef<HTMLInputElement | null>(null);

  const [viewMode, setViewMode] = useState<'list' | 'grid'>('list');
  const [searchQuery, setSearchQuery] = useState('');
  const [sortField, setSortField] = useState<SortField>('name');
  const [sortOrder, setSortOrder] = useState<SortOrder>('asc');
  const [currentPage, setCurrentPage] = useState(1);
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set());
  const [isDraggingOver, setIsDraggingOver] = useState(false);

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

  // Filtered and sorted items (directories always first, locale-aware Intl.Collator)
  const visibleItems = useMemo(() => {
    const raw = searchFilesRecursively(files, activeVolumeId, searchQuery, currentFolderId);
    return sortFileItems(raw, sortField, sortOrder, files, locale);
  }, [files, activeVolumeId, searchQuery, currentFolderId, sortField, sortOrder, locale]);

  const totalPages = Math.max(1, Math.ceil(visibleItems.length / PAGE_SIZE));
  const safePage = Math.min(currentPage, totalPages);
  const paginatedItems = useMemo(() => {
    const start = (safePage - 1) * PAGE_SIZE;
    return visibleItems.slice(start, start + PAGE_SIZE);
  }, [visibleItems, safePage]);

  const fileListSpringStyle = useInterruptibleViewSpring(
    `${activeVolumeId}-${currentFolderId ?? 'root'}-${viewMode}-${safePage}`
  );

  const volumeDirectories = useMemo(
    () => files.filter((f) => f.volumeId === activeVolumeId && f.isDir),
    [files, activeVolumeId]
  );

  const offlineIdsSet = useMemo(
    () => new Set(offlineFiles.map((o) => o.fileId)),
    [offlineFiles]
  );

  const handleVolumeSelect = (volId: string) => {
    onChangeVolume(volId);
    setSelectedIds(new Set());
    setCurrentPage(1);
    setSearchQuery('');
  };

  const handleFolderClick = (folderId: string | null) => {
    onNavigateFolder(folderId);
    setSelectedIds(new Set());
    setCurrentPage(1);
    setSearchQuery('');
  };

  const handleUpOneLevel = () => {
    if (!currentFolderId) return;
    const currentDir = files.find((f) => f.id === currentFolderId);
    handleFolderClick(currentDir?.parentId ?? null);
  };

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
      onUploadFiles(e.target.files, activeVolumeId, currentFolderId);
      e.target.value = '';
    }
  };

  const handleDrop = (e: React.DragEvent<HTMLDivElement>) => {
    e.preventDefault();
    setIsDraggingOver(false);
    if (e.dataTransfer.files && e.dataTransfer.files.length > 0) {
      onUploadFiles(e.dataTransfer.files, activeVolumeId, currentFolderId);
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
    if (!editingItem || !editName.trim()) return;
    onRenameMoveItem(editingItem.id, editName.trim(), editTargetFolderId);
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
    if (item.category === 'image' && item.thumbnailUrl) {
      return (
        <img
          src={item.thumbnailUrl}
          alt={item.name}
          referrerPolicy="no-referrer"
          className="w-11 h-9 rounded-lg object-cover border border-zinc-200 dark:border-zinc-800 shrink-0"
        />
      );
    }
    return <RichFileTypeEmblem category={item.category} size="sm" />;
  };

  return (
    <div
      className="space-y-4"
      onDragOver={(e) => {
        e.preventDefault();
        if (!isDraggingOver) setIsDraggingOver(true);
      }}
      onDragLeave={() => setIsDraggingOver(false)}
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

      {/* Top Row: Volume Switcher & Primary File Actions */}
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
            {volumes.map((v) => {
              const labelAlias = v.alias;
              return (
                <option
                  key={v.id}
                  value={v.id}
                  className="bg-white dark:bg-zinc-900 text-zinc-900 dark:text-zinc-100"
                >
                  {v.name} — {labelAlias}
                </option>
              );
            })}
          </Select>
        </div>

        <div className="flex flex-wrap items-center gap-2">
          <Button
            type="button"
            variant="accent"
            onClick={() => fileInputRef.current?.click()}
          >
            <Upload className="w-3.5 h-3.5" />
            <span>{t.files.uploadFileBtn}</span>
          </Button>

          <Button
            type="button"
            variant="outline"
            onClick={() => dirInputRef.current?.click()}
          >
            <FolderUp className="w-3.5 h-3.5" />
            <span>{t.files.uploadDirBtn}</span>
          </Button>

          <Button
            type="button"
            variant="outline"
            onClick={() => setShowNewFolderModal(true)}
          >
            <FolderPlus className="w-3.5 h-3.5" />
            <span>{t.files.newFolderBtn}</span>
          </Button>
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
              className={`h-8 px-2.5 rounded-md text-xs font-mono transition-colors cursor-pointer ${
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
                    className={`h-8 px-2.5 rounded-md text-xs font-mono truncate max-w-[160px] transition-colors cursor-pointer ${
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
                size="sm"
                onClick={() => setSortOrder((o) => (o === 'asc' ? 'desc' : 'asc'))}
                aria-label={sortOrder === 'asc' ? t.files.sortAsc : t.files.sortDesc}
              >
                {sortOrder === 'asc' ? (
                  <ArrowUp className="w-3.5 h-3.5" />
                ) : (
                  <ArrowDown className="w-3.5 h-3.5" />
                )}
                <span className="hidden sm:inline">
                  {sortOrder === 'asc' ? t.files.sortAsc : t.files.sortDesc}
                </span>
              </Button>

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
        ) : visibleItems.length === 0 ? (
          <div className="p-12 text-center space-y-2">
            <HardDrive className="w-8 h-8 text-zinc-400 mx-auto" />
            <h3 className="text-sm font-semibold text-zinc-900 dark:text-zinc-100">
              {searchQuery.trim() ? t.files.emptySearchTitle : t.files.emptyDirTitle}
            </h3>
            <p className="text-xs text-zinc-500 dark:text-zinc-400 max-w-md mx-auto">
              {searchQuery.trim() ? t.files.emptySearchDesc : t.files.emptyDirDesc}
            </p>
          </div>
        ) : viewMode === 'grid' ? (
          /* GALLERY GRID VIEW */
          <motion.div
            style={fileListSpringStyle}
            className="p-4 grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4 will-change-transform"
          >
            {paginatedItems.map((item) => {
              const isSelected = selectedIds.has(item.id);
              const isOffline = offlineIdsSet.has(item.id);
              const displaySize = item.isDir
                ? computeDirectoryStats(files, item.volumeId, item.id).totalBytes
                : item.size;

              return (
                <div
                  key={item.id}
                  className={`rounded-xl border border-zinc-200/80 dark:border-zinc-800/80 bg-zinc-50/40 dark:bg-zinc-900/40 overflow-hidden flex flex-col justify-between transition-all ${
                    isSelected ? 'ring-2 ring-blue-500' : ''
                  }`}
                >
                  <div>
                    <div className="relative h-32 bg-zinc-900/5 dark:bg-black/40 flex items-center justify-center overflow-hidden border-b border-zinc-200/60 dark:border-zinc-800/60">
                      {item.thumbnailUrl ? (
                        <img
                          src={item.thumbnailUrl}
                          alt={item.name}
                          referrerPolicy="no-referrer"
                          className="w-full h-full object-cover"
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
                        {item.name}
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
                        <Button
                          type="button"
                          variant="ghost"
                          size="icon"
                          onClick={() => onDownloadFile(item)}
                          aria-label={`${t.files.actionDownload} ${item.name}`}
                        >
                          <Download className="w-3.5 h-3.5" />
                        </Button>
                        <Button
                          type="button"
                          variant="ghost"
                          size="icon"
                          onClick={() => onSaveOffline(item)}
                          disabled={isOffline}
                          aria-label={`${t.files.actionSaveOffline} ${item.name}`}
                        >
                          <HardDrive className="w-3.5 h-3.5" />
                        </Button>
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
          <motion.div style={fileListSpringStyle} className="will-change-transform">
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
                const isOffline = offlineIdsSet.has(item.id);
                const displaySize = item.isDir
                  ? computeDirectoryStats(files, item.volumeId, item.id).totalBytes
                  : item.size;

                return (
                  <div
                    key={item.id}
                    className={`p-3 md:px-4 md:py-2.5 flex flex-col md:grid md:grid-cols-12 md:items-center gap-2.5 transition-colors ${
                      isSelected
                        ? 'bg-blue-500/[0.06] dark:bg-blue-500/[0.09]'
                        : 'hover:bg-zinc-50/80 dark:hover:bg-white/[0.02]'
                    }`}
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
                          {item.name}
                        </button>

                        <div className="flex flex-wrap items-center gap-1.5 text-xs tabular-nums text-zinc-500 dark:text-zinc-400 mt-0.5">
                          <span>{getCategoryLabel(item.category, item.isDir)}</span>
                          {searchQuery.trim() && (
                            <>
                              <span aria-hidden="true">·</span>
                              <span dir="ltr" className="truncate max-w-[180px] font-mono">
                                {getItemFullPath(files, item)}
                              </span>
                            </>
                          )}
                          {isOffline && (
                            <>
                              <span aria-hidden="true">·</span>
                              <span className="text-emerald-600 dark:text-emerald-400">
                                {t.files.actionOfflineSaved}
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
                          <Button
                            type="button"
                            variant="ghost"
                            size="icon"
                            onClick={() => onSaveOffline(item)}
                            disabled={isOffline}
                            aria-label={`${t.files.actionSaveOffline} ${item.name}`}
                            title={
                              isOffline ? t.files.actionOfflineSaved : t.files.actionSaveOffline
                            }
                          >
                            <HardDrive className="w-3.5 h-3.5" />
                          </Button>
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

        {/* Bottom Drag-and-Drop Strip + Pagination Footer */}
        <div className="px-4 py-3 bg-zinc-50/70 dark:bg-zinc-900/40 border-t border-zinc-200/80 dark:border-zinc-800/80 flex flex-col sm:flex-row sm:items-center justify-between gap-3">
          <div
            onClick={() => fileInputRef.current?.click()}
            role="button"
            tabIndex={0}
            onKeyDown={(e) => {
              if (e.key === 'Enter' || e.key === ' ') {
                e.preventDefault();
                fileInputRef.current?.click();
              }
            }}
            className={`px-2.5 py-1.5 rounded-lg text-xs cursor-pointer inline-flex items-center gap-2 transition-colors ${
              isDraggingOver
                ? 'bg-blue-500/15 text-blue-600 dark:text-blue-300'
                : 'text-zinc-500 dark:text-zinc-400 hover:text-zinc-900 dark:hover:text-zinc-200'
            }`}
          >
            <Upload className="w-3.5 h-3.5 shrink-0" />
            <span>{t.files.dropzoneHint}</span>
          </div>

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

      {/* Modal 1: Create New Folder */}
      {showNewFolderModal && (
        <div
          className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-xs p-4"
          role="dialog"
          aria-modal="true"
          aria-labelledby="modal-new-folder-title"
        >
          <form
            onSubmit={submitNewFolder}
            className="w-full max-w-md rounded-xl surface-card p-6 space-y-4 shadow-xl"
          >
            <h3
              id="modal-new-folder-title"
              className="text-base font-semibold text-zinc-950 dark:text-white"
            >
              {t.files.modalNewFolderTitle}
            </h3>
            <div className="space-y-1.5">
              <label
                htmlFor="new-folder-name-input"
                className="block text-xs font-medium text-zinc-700 dark:text-zinc-300"
              >
                {t.files.modalNewFolderLabel}
              </label>
              <Input
                id="new-folder-name-input"
                type="text"
                required
                autoFocus
                value={newFolderName}
                onChange={(e) => setNewFolderName(e.target.value)}
                placeholder={t.files.modalNewFolderPlaceholder}
              />
            </div>
            <div className="flex items-center justify-end gap-2 pt-2">
              <Button
                type="button"
                variant="outline"
                onClick={() => setShowNewFolderModal(false)}
              >
                {t.files.cancel}
              </Button>
              <Button type="submit" variant="accent">
                {t.files.create}
              </Button>
            </div>
          </form>
        </div>
      )}

      {/* Modal 2: Rename / Move Single Item */}
      {editingItem && (
        <div
          className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-xs p-4"
          role="dialog"
          aria-modal="true"
          aria-labelledby="modal-rename-move-title"
        >
          <form
            onSubmit={submitRenameMove}
            className="w-full max-w-md rounded-xl surface-card p-6 space-y-4 shadow-xl"
          >
            <h3
              id="modal-rename-move-title"
              className="text-base font-semibold text-zinc-950 dark:text-white"
            >
              {t.files.modalRenameMoveTitle}
            </h3>
            <div className="space-y-1.5">
              <label
                htmlFor="rename-item-input"
                className="block text-xs font-medium text-zinc-700 dark:text-zinc-300"
              >
                {t.files.modalRenameLabel}
              </label>
              <Input
                id="rename-item-input"
                type="text"
                required
                value={editName}
                onChange={(e) => setEditName(e.target.value)}
              />
            </div>

            <div className="space-y-1.5">
              <label
                htmlFor="move-target-select"
                className="block text-xs font-medium text-zinc-700 dark:text-zinc-300"
              >
                {t.files.modalMoveTargetLabel}
              </label>
              <Select
                id="move-target-select"
                value={editTargetFolderId ?? '__ROOT__'}
                onChange={(e) =>
                  setEditTargetFolderId(e.target.value === '__ROOT__' ? null : e.target.value)
                }
                className="w-full"
              >
                <option value="__ROOT__" className="bg-white dark:bg-zinc-900">
                  /{currentVolume?.name} ({t.files.rootCrumb})
                </option>
                {volumeDirectories
                  .filter((dir) => canMoveItemsToFolder(files, [editingItem.id], dir.id))
                  .map((dir) => (
                    <option key={dir.id} value={dir.id} className="bg-white dark:bg-zinc-900">
                      {getItemFullPath(files, dir)}
                    </option>
                  ))}
              </Select>
            </div>

            <div className="flex items-center justify-end gap-2 pt-2">
              <Button
                type="button"
                variant="outline"
                onClick={() => setEditingItem(null)}
              >
                {t.files.cancel}
              </Button>
              <Button type="submit" variant="accent">
                {t.files.save}
              </Button>
            </div>
          </form>
        </div>
      )}

      {/* Modal 3: Batch Move */}
      {showBatchMoveModal && (
        <div
          className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-xs p-4"
          role="dialog"
          aria-modal="true"
          aria-labelledby="modal-batch-move-title"
        >
          <form
            onSubmit={submitBatchMove}
            className="w-full max-w-md rounded-xl surface-card p-6 space-y-4 shadow-xl"
          >
            <h3
              id="modal-batch-move-title"
              className="text-base font-semibold text-zinc-950 dark:text-white"
            >
              {t.files.modalBatchMoveTitle}
            </h3>
            <div className="space-y-1.5">
              <label
                htmlFor="batch-move-target-select"
                className="block text-xs font-medium text-zinc-700 dark:text-zinc-300"
              >
                {t.files.modalMoveTargetLabel}
              </label>
              <Select
                id="batch-move-target-select"
                value={batchTargetFolderId ?? '__ROOT__'}
                onChange={(e) =>
                  setBatchTargetFolderId(e.target.value === '__ROOT__' ? null : e.target.value)
                }
                className="w-full"
              >
                <option value="__ROOT__" className="bg-white dark:bg-zinc-900">
                  /{currentVolume?.name} ({t.files.rootCrumb})
                </option>
                {volumeDirectories
                  .filter((dir) => canMoveItemsToFolder(files, Array.from(selectedIds), dir.id))
                  .map((dir) => (
                    <option key={dir.id} value={dir.id} className="bg-white dark:bg-zinc-900">
                      {getItemFullPath(files, dir)}
                    </option>
                  ))}
              </Select>
            </div>
            <div className="flex items-center justify-end gap-2 pt-2">
              <Button
                type="button"
                variant="outline"
                onClick={() => setShowBatchMoveModal(false)}
              >
                {t.files.cancel}
              </Button>
              <Button type="submit" variant="accent">
                {t.files.confirm}
              </Button>
            </div>
          </form>
        </div>
      )}

      {/* Modal 4: Delete Confirmation */}
      {deleteConfirmIds && deleteConfirmIds.length > 0 && (
        <div
          className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-xs p-4"
          role="dialog"
          aria-modal="true"
          aria-labelledby="modal-delete-confirm-title"
        >
          <div className="w-full max-w-md rounded-xl surface-card p-6 space-y-4 shadow-xl">
            <h3
              id="modal-delete-confirm-title"
              className="text-base font-semibold text-zinc-950 dark:text-white"
            >
              {t.files.modalConfirmDeleteTitle}
            </h3>
            <p className="text-xs text-zinc-600 dark:text-zinc-400 leading-relaxed">
              {deleteConfirmIds.length === 1
                ? interpolate(
                    t.files.modalConfirmDeleteDesc,
                    {
                      name: files.find((f) => f.id === deleteConfirmIds[0])?.name || '',
                    },
                    locale
                  )
                : formatPlural(deleteConfirmIds.length, t.plurals.batchDeleteConfirm, locale)}
            </p>
            <div className="flex items-center justify-end gap-2 pt-2">
              <Button
                type="button"
                variant="outline"
                onClick={() => setDeleteConfirmIds(null)}
              >
                {t.files.cancel}
              </Button>
              <Button type="button" variant="destructive" onClick={confirmDelete}>
                {t.files.deleteConfirmBtn}
              </Button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
};
