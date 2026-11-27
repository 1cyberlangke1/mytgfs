import React, { Suspense, lazy, useEffect, useMemo, useRef, useState } from 'react';
import { FolderTree, HardDriveDownload, LayoutDashboard, Moon, Monitor, Sliders, Smartphone, Sun } from 'lucide-react';
import { motion } from 'motion/react';
import type { DownloadItem, FileItem, Locale, NavTab, ThemeMode } from './types';
import { computeOverviewSummary } from './utils/fsLogic';
import { loadShortcuts, saveShortcuts, type ShortcutMap } from './utils/shortcuts';
import { mergeTransfers } from './utils/transfers';
import { platformCaps } from './platform';
import { DICTIONARY, LOCALE_OPTIONS, getLocaleMeta, i18next, type Translations } from './utils/i18n';
import { localFileURL, saveSettings } from './api';
import { AccountCard, SettingsView } from './components/SettingsView';
import { TextFileDialog } from './components/TextFileDialog';
import { UploadPlanDialog } from './components/UploadPlanDialog';
import { ConflictDialog } from './components/ConflictDialog';
import { ProblemBanner } from './components/ProblemBanner';
import { ToastStack } from './components/ToastStack';
import { useInterruptibleViewSpring } from './components/ui';
import { WindowCustomScrollbar } from './components/CustomScrollbar';
import { AppSidebar } from './components/AppSidebar';
import { PhoneTabBar, PhoneTopBar } from './components/PhoneChrome';
import { STORAGE_KEYS, loadFromStorage, saveToStorage, urlParam } from './utils/storage';
import { useToasts } from './hooks/useToasts';
import { useProblems } from './hooks/useProblems';
import { useThemeMode } from './hooks/useThemeMode';
import { useKeyboardNavigation } from './hooks/useKeyboardNavigation';
import { useDownloads, useFileActions, useVolumeActions } from './hooks/useActions';
import { useUploads } from './hooks/useUploads';
import { usePanelData } from './hooks/usePanelData';

// 按页签懒加载：切换页签时再取对应 chunk（首屏只带壳与概览之外的公共部分）
const OverviewView = lazy(() => import('./components/OverviewView').then((m) => ({ default: m.OverviewView })));
const FilesView = lazy(() => import('./components/FilesView').then((m) => ({ default: m.FilesView })));
const DownloadsView = lazy(() => import('./components/DownloadsView').then((m) => ({ default: m.DownloadsView })));
const FilePreviewModal = lazy(() => import('./components/FilePreviewModal').then((m) => ({ default: m.FilePreviewModal })));

export default function App() {
  const [activeTab, setActiveTab] = useState<NavTab>(() => {
    const tab = urlParam('tab');
    if (tab === 'overview' || tab === 'files' || tab === 'settings' || (tab === 'downloads' && platformCaps.downloads)) return tab;
    // localStorage 里可能残留旧版本/手改的非法值：只接受白名单，否则白屏
    const saved = loadFromStorage<NavTab | null>(STORAGE_KEYS.tab, null);
    if (saved === 'overview' || saved === 'files' || saved === 'settings') return saved;
    if (saved === 'downloads' && platformCaps.downloads) return saved;
    return 'overview';
  });
  const [locale, setLocale] = useState<Locale>(() => {
    const saved = loadFromStorage<Locale | null>(STORAGE_KEYS.locale, null);
    return saved && DICTIONARY[saved] ? saved : 'zh';
  });
  const { theme, setTheme } = useThemeMode();
  // 键盘快捷键：设置页可改，存 localStorage，文件页读同一份映射
  const [shortcuts, setShortcuts] = useState<ShortcutMap>(() => loadShortcuts());
  const handleChangeShortcuts = (next: ShortcutMap) => {
    saveShortcuts(next);
    setShortcuts(next);
  };
  useKeyboardNavigation(shortcuts);

  const [previewFile, setPreviewFile] = useState<FileItem | null>(null);
  const [localPreview, setLocalPreview] = useState<DownloadItem | null>(null);
  const [sidebarCollapsed, setSidebarCollapsed] = useState(false);
  const { toasts, pushToast, dismissToast } = useToasts();
  // 常驻错误：后台拉取失败、实时连接断开这类「用户必须知道」的问题用横幅，几秒消失的 toast 不够
  const { problemList, setProblems, setProblem, clearProblem } = useProblems();

  const t: Translations = DICTIONARY[locale] ?? DICTIONARY.zh!;
  const localeMeta = getLocaleMeta(locale);
  // 文案与语言用 ref 读取：后台回调不因切语言而重建连接或定时器
  const tRef = useRef(t);
  tRef.current = t;
  const localeRef = useRef(locale);
  localeRef.current = locale;

  const downloadsApi = useDownloads({ activeTab, t, locale, pushToast, dismissToast, setProblem, clearProblem, localPreview, setLocalPreview });
  const {
    downloads, downloadTasks, downloadsDir, reloadDownloads, downloadedPreview,
    startFileDownload, controlTask, removeFile, openDir, exportDir, previewDownloaded, openFile,
  } = downloadsApi;

  const panel = usePanelData({ tRef, localeRef, setProblem, clearProblem, reloadDownloads });
  const {
    volumes, files, filesLoading, settings, setSettings, caps, loginReady, connectionStatus,
    serverTransfers, serverQueue, setServerQueue, activeVolumeId, selectVolume, currentFolderId,
    setCurrentFolderId, reloadState, reloadFiles, reloadSettings, onConnectedRef,
  } = panel;

  const uploads = useUploads({
    t, locale, tRef, localeRef, activeTab, activeVolumeId, currentFolderId, files,
    pushToast, setServerQueue, onConnectedRef, reloadFiles, reloadState,
  });
  const {
    uploadTasks, stagedUploads, pendingConflicts, textDraft, setTextDraft,
    handleUploadSources, handleUploadFiles, handleImportFolder, handleImportFiles,
    handleConfirmUpload, handleResolveConflicts, handleCancelConflicts, handleCancelUploadPlan,
    handleTextFileSave, handleClipboardUpload, handleToggleTransferPause, handleRetryTransfer,
    handleCancelTransfer, handleCancelQueueJob, handleClearCompletedTransfers,
  } = uploads;

  const summary = computeOverviewSummary(volumes, files);
  const viewSpring = useInterruptibleViewSpring(activeTab);
  const { handleCreateFolder, handleRenameMoveItem, handleBatchMoveItems, handleDeleteItems } = useFileActions({
    files, t, locale, pushToast, dismissToast, reloadFiles,
  });
  const {
    handleCreateVolume, handleAttachVolume, handleDeleteVolume, handleSetCover,
    handlePickCoverNative, handleSetVolumeTitle, handleRebuildVolume,
  } = useVolumeActions({ t, locale, pushToast, dismissToast, reloadState, reloadFiles });

  // 合并上传中、下载中与服务端队列任务（规则见 utils/transfers.ts）
  const transfers = useMemo(() => mergeTransfers(uploadTasks, serverTransfers, serverQueue), [uploadTasks, serverTransfers, serverQueue]);

  // 首次配置（还没有 api_id / api_hash）：直接落到设置页，不能让用户自己找
  useEffect(() => {
    if (loginReady === false && caps.login) setActiveTab('settings');
  }, [loginReady, caps.login]);

  // retryLoad 手动重试全部后台加载；失败会在各自回调里重新写回横幅
  const retryLoad = () => {
    setProblems({});
    void reloadState();
    void reloadFiles();
    void reloadSettings();
    void reloadDownloads();
  };

  useEffect(() => {
    saveToStorage(STORAGE_KEYS.tab, activeTab);
  }, [activeTab]);

  useEffect(() => {
    saveToStorage(STORAGE_KEYS.locale, locale);
    const meta = getLocaleMeta(locale);
    document.documentElement.lang = meta.htmlLang;
    document.documentElement.dir = meta.dir;
    document.title = DICTIONARY[locale]?.pageTitle || 'mytgfs';
    if (i18next.language !== locale) i18next.changeLanguage(locale).catch(() => {});
  }, [locale]);

  useEffect(() => {
    if (activeVolumeId) saveToStorage(STORAGE_KEYS.activeVol, activeVolumeId);
  }, [activeVolumeId]);

  // handleSaveSettings 只提交卡片里的键，成功后本地合并，不再整页重拉
  const handleSaveSettings = (values: Record<string, string>) =>
    saveSettings(values).then(() => setSettings((prev) => ({ ...prev, values: { ...prev.values, ...values } })));

  // handleRefresh 手动刷新状态、文件列表与下载任务
  const handleRefresh = async (): Promise<void> => {
    const names = await reloadState();
    if (names && names.length > 0) await reloadFiles(names);
    await reloadDownloads();
  };

  const navAll: { id: NavTab; label: string; icon: React.ReactNode }[] = [
    { id: 'overview', label: t.nav.overview, icon: <LayoutDashboard className="w-4 h-4 shrink-0" /> },
    { id: 'files', label: t.nav.files, icon: <FolderTree className="w-4 h-4 shrink-0" /> },
    { id: 'settings', label: t.nav.settings, icon: <Sliders className="w-4 h-4 shrink-0" /> },
    { id: 'downloads', label: t.nav.downloads, icon: <HardDriveDownload className="w-4 h-4 shrink-0" /> },
  ];
  const navItems = navAll.filter((item) => item.id !== 'downloads' || platformCaps.downloads);

  // 主题按钮显示当前档位，点一下切到下一档：浅色 → 深色 → 跟随系统
  const themeNext: ThemeMode = theme === 'light' ? 'dark' : theme === 'dark' ? 'system' : 'light';
  const ThemeIcon = theme === 'light' ? Sun : theme === 'dark' ? Moon : Monitor;
  // 手机顶栏的「跟随系统」用手机图标：只在够宽（桌面侧栏）时才用显示器
  const PhoneThemeIcon = theme === 'light' ? Sun : theme === 'dark' ? Moon : Smartphone;
  const themeLabel = theme === 'light' ? t.header.themeLight : theme === 'dark' ? t.header.themeDark : t.header.themeSystem;
  const themeIconClass = `shrink-0 ${theme === 'light' ? 'text-amber-400' : 'text-zinc-700 dark:text-zinc-300'}`;
  const cycleNextLocale = () => {
    const idx = LOCALE_OPTIONS.findIndex((o) => o.id === locale);
    setLocale(LOCALE_OPTIONS[(idx + 1) % LOCALE_OPTIONS.length]!.id);
  };

  return (
    <div dir={localeMeta.dir} className="min-h-screen flex flex-col bg-[#F5F5F7] dark:bg-[#090A0F] text-zinc-900 dark:text-zinc-100">
      {/* 纯键盘用户按 Tab 第一下就跳到正文，跳过整排导航 */}
      <a
        href="#main"
        className="sr-only focus:not-sr-only focus:fixed focus:top-2 focus:start-2 focus:z-50 focus:px-3 focus:py-2 focus:rounded-lg focus:bg-white dark:focus:bg-zinc-900 focus:text-xs focus:font-medium focus:shadow-lg"
      >
        {t.header.skipToContent}
      </a>

      <PhoneTopBar
        t={t}
        locale={locale}
        setLocale={setLocale}
        totalBytes={summary.totalBytes}
        setActiveTab={setActiveTab}
        connectionStatus={connectionStatus}
        themeNext={themeNext}
        themeLabel={themeLabel}
        themeIconClass={themeIconClass}
        ThemeIcon={PhoneThemeIcon}
        setTheme={setTheme}
      />

      <div className="flex-1 flex min-w-0">
        <AppSidebar
          t={t}
          locale={locale}
          setLocale={setLocale}
          localeMeta={localeMeta}
          cycleNextLocale={cycleNextLocale}
          navItems={navItems}
          activeTab={activeTab}
          setActiveTab={setActiveTab}
          sidebarCollapsed={sidebarCollapsed}
          setSidebarCollapsed={setSidebarCollapsed}
          summary={summary}
          downloadsCount={downloads.length + downloadTasks.length}
          activeVolumeId={activeVolumeId}
          selectVolume={selectVolume}
          setCurrentFolderId={setCurrentFolderId}
          connectionStatus={connectionStatus}
          themeNext={themeNext}
          themeLabel={themeLabel}
          themeIconClass={themeIconClass}
          ThemeIcon={ThemeIcon}
          setTheme={setTheme}
        />

        <WindowCustomScrollbar />

        <main id="main" tabIndex={-1} className="flex-1 min-w-0 px-3.5 sm:px-6 lg:px-8 pt-4 sm:pt-7 pb-24 md:pb-8 max-w-5xl mx-auto w-full outline-none">
          <ProblemBanner
            messages={problemList}
            title={t.errors.title}
            retryLabel={t.errors.retry}
            dismissLabel={t.errors.dismiss}
            onRetry={retryLoad}
            onDismiss={() => setProblems({})}
          />
          <motion.div style={{ y: viewSpring.y, scale: viewSpring.scale, opacity: viewSpring.opacity, transformOrigin: 'top center' }}>
            <Suspense fallback={<div className="py-16" aria-hidden="true" />}>
              {activeTab === 'overview' && (
                <OverviewView
                  t={t}
                  locale={locale}
                  volumes={volumes}
                  files={files}
                  transfers={transfers}
                  activeVolumeId={activeVolumeId}
                  onSelectVolumeAndBrowse={(volId) => {
                    selectVolume(volId);
                    setCurrentFolderId(null);
                    setActiveTab('files');
                  }}
                  onUploadFiles={handleUploadFiles}
                  onToggleTransferPause={handleToggleTransferPause}
                  onRetryTransfer={handleRetryTransfer}
                  onCancelTransfer={handleCancelTransfer}
                  onCancelQueueJob={handleCancelQueueJob}
                  onClearCompletedTransfers={handleClearCompletedTransfers}
                  onSetCover={caps.cover ? handleSetCover : undefined}
                  onSetTitle={caps.cover ? handleSetVolumeTitle : undefined}
                  onPickCoverNative={caps.cover && platformCaps.coverPicker ? handlePickCoverNative : undefined}
                />
              )}

              {activeTab === 'files' && (
                <FilesView
                  t={t}
                  locale={locale}
                  volumes={volumes}
                  activeVolumeId={activeVolumeId}
                  onChangeVolume={(volId) => {
                    selectVolume(volId);
                    setCurrentFolderId(null);
                  }}
                  currentFolderId={currentFolderId}
                  onNavigateFolder={setCurrentFolderId}
                  files={files}
                  loading={filesLoading}
                  connectionStatus={connectionStatus}
                  onUploadSources={handleUploadSources}
                  onImportFiles={(volumeId, dir) => void handleImportFiles(volumeId, dir)}
                  onImportFolder={(volumeId, dir) => void handleImportFolder(volumeId, dir)}
                  canPickPaths={platformCaps.pickPaths}
                  onNewTextFile={() => setTextDraft({ initial: '' })}
                  onPasteClipboard={() => void handleClipboardUpload()}
                  onNotifyError={(msg) => pushToast(msg, 'error')}
                  onCreateFolder={handleCreateFolder}
                  onRenameMoveItem={handleRenameMoveItem}
                  onBatchMoveItems={handleBatchMoveItems}
                  onMoveItems={handleBatchMoveItems}
                  onDeleteItems={handleDeleteItems}
                  onDownloadFile={startFileDownload}
                  onPreviewFile={setPreviewFile}
                  canDownload={platformCaps.downloads}
                  canPaste={platformCaps.paste}
                  canCreateVolume={caps.createVolume}
                  canDeleteVolume={caps.deleteVolume}
                  canAttachVolume={caps.attachVolume}
                  canListChannels={caps.listChannels}
                  onAttachVolume={handleAttachVolume}
                  onCreateVolume={handleCreateVolume}
                  onDeleteVolume={handleDeleteVolume}
                  onRefresh={handleRefresh}
                  onRebuildVolume={caps.rebuild ? handleRebuildVolume : undefined}
                  shortcuts={shortcuts}
                />
              )}

              {activeTab === 'settings' && (
                <div className="space-y-8">
                  {caps.login && <AccountCard t={t} onNotify={pushToast} />}
                  <SettingsView
                    t={t}
                    locale={locale}
                    settings={settings}
                    canAutostart={caps.autostart && platformCaps.autostart}
                    canUseShortcuts={platformCaps.shortcuts}
                    canSetDownloadsDir={platformCaps.downloadsDirConfigurable}
                    shortcuts={shortcuts}
                    onChangeShortcuts={handleChangeShortcuts}
                    onSaveSettings={handleSaveSettings}
                    onNotifyError={(msg) => pushToast(msg, 'error')}
                  />
                </div>
              )}

              {platformCaps.downloads && activeTab === 'downloads' && (
                <DownloadsView
                  t={t}
                  locale={locale}
                  dir={downloadsDir}
                  files={downloads}
                  tasks={downloadTasks}
                  onRefresh={() => void reloadDownloads()}
                  dirAction={platformCaps.downloadsDirAction}
                  onDirAction={platformCaps.downloadsDirAction === 'open' ? openDir : exportDir}
                  onRemove={removeFile}
                  onTask={controlTask}
                  onPreview={previewDownloaded}
                />
              )}
            </Suspense>
          </motion.div>
        </main>
      </div>

      <PhoneTabBar
        t={t}
        locale={locale}
        navItems={navItems}
        activeTab={activeTab}
        setActiveTab={setActiveTab}
        totalFiles={summary.totalFiles}
        downloadsCount={downloads.length + downloadTasks.length}
      />

      <Suspense fallback={null}>
        <FilePreviewModal
          file={previewFile}
          allFiles={files}
          t={t}
          locale={locale}
          onClose={() => setPreviewFile(null)}
          onDownload={startFileDownload}
          canDownload={platformCaps.downloads}
        />
        {/* 下载页本地预览：媒体源指向 /api/downloads/file，主按钮交给系统默认程序 */}
        <FilePreviewModal
          file={downloadedPreview}
          allFiles={[]}
          t={t}
          locale={locale}
          onClose={() => setLocalPreview(null)}
          onDownload={(f) => void openFile(f.name)}
          streamOverride={localPreview ? localFileURL(localPreview.name) : undefined}
          actionLabel={t.downloads.openBtn}
        />
      </Suspense>

      {/* 上传前清单：确认之前不发任何请求，移除的文件直接作废 */}
      {stagedUploads.length > 0 && <UploadPlanDialog t={t} locale={locale} items={stagedUploads} onConfirm={handleConfirmUpload} onCancel={handleCancelUploadPlan} />}

      {/* 同名冲突：确认处理后才会真正入队 */}
      {pendingConflicts !== null && (
        <ConflictDialog t={t} items={pendingConflicts.items} conflicts={pendingConflicts.conflicts} onConfirm={handleResolveConflicts} onCancel={handleCancelConflicts} />
      )}

      {/* 新建文本：保存后走普通上传清单 */}
      {textDraft !== null && <TextFileDialog t={t} initialText={textDraft.initial} onSave={handleTextFileSave} onClose={() => setTextDraft(null)} />}

      <ToastStack
        toasts={toasts}
        dismissLabel={t.a11y.dismissToast}
        onDismiss={dismissToast}
        onAction={(toast) => {
          toast.action?.onClick();
          dismissToast(toast.id);
        }}
      />
    </div>
  );
}
