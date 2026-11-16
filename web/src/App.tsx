import React, { Suspense, lazy, useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  FolderTree,
  HardDriveDownload,
  LayoutDashboard,
  Moon,
  Monitor,
  Sliders,
  Sun,
} from 'lucide-react';
import { motion } from 'motion/react';
import {
  AppSettings,
  ConnectionStatus,
  DownloadItem,
  DownloadTask,
  FileCategory,
  FileItem,
  Locale,
  NavTab,
  ThemeMode,
  TransferTask,
  VolumeInfo,
} from './types';
import {
  computeOverviewSummary,
  inferFileCategory,
  pickActiveVolume,
  validateAndPlanUpload,
} from './utils/fsLogic';
import { loadShortcuts, matchesShortcut, saveShortcuts, type ShortcutMap } from './utils/shortcuts';
import {
  activateActive,
  focusAdjacent,
  focusPrimaryNav,
  moveFocusDirection,
  scrollActiveByPage,
  scrollActiveToEdge,
} from './utils/keynav';
import { subscribePush } from './events';
import {
  DICTIONARY,
  LOCALE_OPTIONS,
  Translations,
  getLocaleMeta,
  i18next,
  interpolate,
} from './utils/i18n';
import {
  ApiFile,
  ApiQueueItem,
  ApiState,
  ApiTransfer,
  UploadResume,
  UploadPaused,
  attachVolume,
  discardUpload,
  clearQueue,
  createVolume,
  deleteVolume,
  cancelDownload,
  pauseDownload,
  resumeDownload,
  startDownload,
  getDownloads,
  getSettings,
  getState,
  listFiles,
  localFileURL,
  mkdir,
  moveItems,
  openDownloadedFile,
  openDownloads,
  removeDownload,
  removeItems,
  saveSettings,
  rebuildVolume,
  setCover,
  setVolumeTitle,
  uploadFile,
  uploadPoster,
} from './api';
import { AccountCard } from './components/AccountCard';
import { TextFileDialog } from './components/TextFileDialog';
import { UploadPlanDialog } from './components/UploadPlanDialog';
import { ProblemBanner } from './components/ProblemBanner';
import { ToastStack, type ToastMessage } from './components/ToastStack';
import {
  useInterruptibleViewSpring,
} from './components/ui';
import { WindowCustomScrollbar } from './components/CustomScrollbar';
import { AppSidebar } from './components/AppSidebar';
import { PhoneTabBar, PhoneTopBar } from './components/PhoneChrome';
import { makeVideoPoster } from './utils/poster';
import { sourcesFromFiles, stageSources, toTransferTasks, type StagedUpload, type UploadSource } from './utils/uploadPlan';
import { extractPasteContent, isEditableTarget } from './utils/paste';

// 按页签懒加载：首屏只带壳与概览之外的公共部分，切换页签时再取对应 chunk。
// 输入：无；输出：React.lazy 组件；预期：vite 按动态 import 自动分包，首包体积下降。
const OverviewView = lazy(() => import('./components/OverviewView').then((m) => ({ default: m.OverviewView })));
const FilesView = lazy(() => import('./components/FilesView').then((m) => ({ default: m.FilesView })));
const SettingsView = lazy(() => import('./components/SettingsView').then((m) => ({ default: m.SettingsView })));
const DownloadsView = lazy(() => import('./components/DownloadsView').then((m) => ({ default: m.DownloadsView })));
const FilePreviewModal = lazy(() => import('./components/FilePreviewModal').then((m) => ({ default: m.FilePreviewModal })));

const STORAGE_KEYS = {
  tab: 'mytgfs_active_tab_v1',
  locale: 'mytgfs_locale_v1',
  theme: 'mytgfs_theme_v1',
  activeVol: 'mytgfs_active_vol_v1',
};

// MAX_PARALLEL_UPLOADS 是客户端同时在传的文件数。
//
// 服务端每个分片会话都有空闲回收，一次性放太多文件会撞 404；
// 3 路既能把带宽跑满，也留得住会话。
const MAX_PARALLEL_UPLOADS = 3;

function loadFromStorage<T>(key: string, fallback: T): T {
  if (typeof window === 'undefined') return fallback;
  try {
    const raw = window.localStorage.getItem(key);
    if (!raw) return fallback;
    return JSON.parse(raw) as T;
  } catch {
    return fallback;
  }
}

// urlParam 读取托盘「按卷快速打开」带进来的查询参数。
//
// 输入：name 参数名
// 输出：参数值；没有则为空串
// 预期：只用于首屏初始化，之后切页仍由本地状态与 localStorage 决定
function urlParam(name: string): string {
  if (typeof window === 'undefined') return '';
  return new URLSearchParams(window.location.search).get(name) ?? '';
}

// markEditable 把「正在编辑」映射到 readOnly，让键盘用户与读屏都知道此刻能不能输入。
//
// 输入：元素（可能是 null）；editing 是否已进入编辑态
// 输出：无
// 预期：只有文本框/多行文本参与；未进入编辑时保持 readOnly，字符不会落进框里
function markEditable(el: Element | null, editing: boolean) {
  if (!el) return;
  if (el.tagName !== 'INPUT' && el.tagName !== 'TEXTAREA') return;
  (el as HTMLInputElement | HTMLTextAreaElement).readOnly = !editing;
}

// isTextField 判断控件是否是「接受文字」的输入框。
//
// 输入：任意元素（可为 null）
// 输出：文本框/多行文本/可编辑区为 true；勾选框、下拉、按钮等为 false
// 预期：只有文本框需要 Enter 才能编辑；勾选框仍由 Enter 直接切换
function isTextField(el: HTMLElement | null): boolean {
  if (!el) return false;
  if (el.isContentEditable) return true;
  if (el.tagName === 'TEXTAREA') return true;
  if (el.tagName !== 'INPUT') return false;
  switch ((el as HTMLInputElement).type) {
    case 'search':
    case 'password':
    case 'number':
    case 'email':
    case 'url':
    case 'tel':
    case 'text':
      return true;
    default:
      return false;
  }
}

// EMPTY_SETTINGS 是服务端设置到达前的占位；不携带任何默认数字，默认值只由服务端给。
const EMPTY_SETTINGS: AppSettings = {
  schema: [],
  values: {},
};

// toFileItem 把服务端条目转成前端展示结构。
function toFileItem(f: ApiFile): FileItem {
  return {
    id: f.id,
    volumeId: f.volumeId,
    parentId: f.parentId,
    name: f.name,
    isDir: f.isDir,
    size: f.size,
    updatedAt: f.updatedAt,
    category: f.category as FileCategory,
    thumbnailUrl: f.thumbnailUrl,
    previewUrl: f.previewUrl,
  };
}

// transferFromServer 把服务端下载进度转成传输任务。
function transferFromServer(t: ApiTransfer, index: number): TransferTask {
  const volumeId = t.path.includes(':') ? t.path.slice(0, t.path.indexOf(':')) : '';
  const name = t.path.includes(':') ? t.path.slice(t.path.indexOf(':') + 1) : t.path;
  return {
    id: `dl-${index}-${t.path}`,
    fileName: name.split('/').pop() || name,
    volumeId,
    targetParentId: null,
    direction: 'download',
    size: t.total,
    transferredBytes: t.done,
    state: 'transferring',
    speedBps: 0,
    isChunkedResume: false,
    chunkSize: 0,
    currentChunk: 0,
    totalChunks: 0,
    createdAt: new Date().toISOString(),
    category: inferFileCategory(name, false),
    opKind: 'download',
  };
}

// queueFromServer 把服务端队列快照转成传输任务。
//
// 输入：q 队列项（volume/kind/name/done/size 由服务端给出）；index 序号
// 输出：按服务端 state 映射的传输任务（done→completed，failed 带原因），
//       带已传字节与总字节，进度条据此显示真实百分比
// 预期：已完成的活儿必须显示「已完成」，不能一律当成排队中
function queueFromServer(q: ApiQueueItem, index: number): TransferTask {
  const volumeId = q.volume || '';
  const kind = (q.kind || 'upload') as TransferTask['opKind'];
  const state: TransferTask['state'] =
    q.state === 'done'
      ? 'completed'
      : q.state === 'failed'
      ? 'failed'
      : q.state === 'running'
      ? 'transferring'
      : 'queued';
  return {
    id: `q-${index}-${q.id}`,
    fileName: q.name.split('/').pop() || q.name,
    volumeId,
    targetParentId: null,
    direction: kind === 'download' ? 'download' : 'upload',
    size: q.size ?? 0,
    transferredBytes: q.done ?? 0,
    state,
    speedBps: 0,
    isChunkedResume: false,
    chunkSize: 0,
    currentChunk: 0,
    totalChunks: 0,
    createdAt: new Date().toISOString(),
    category: inferFileCategory(q.name, false),
    errorMessage: q.error,
    opKind: kind,
  };
}

export default function App() {
  const globalUploadRef = useRef<HTMLInputElement | null>(null);

  const [activeTab, setActiveTab] = useState<NavTab>(() => {
    const tab = urlParam('tab');
    if (tab === 'overview' || tab === 'files' || tab === 'settings' || tab === 'downloads') {
      return tab;
    }
    return loadFromStorage<NavTab>(STORAGE_KEYS.tab, 'overview');
  });
  const [locale, setLocale] = useState<Locale>(() => {
    const saved = loadFromStorage<Locale | null>(STORAGE_KEYS.locale, null);
    if (saved && DICTIONARY[saved]) return saved;
    return 'zh';
  });
  const [theme, setTheme] = useState<ThemeMode>(() => {
    const saved = loadFromStorage<ThemeMode | null>(STORAGE_KEYS.theme, null);
    // 缺省跟随系统；旧版本只存过 light/dark，读到非法值也回落到跟随系统
    return saved === 'light' || saved === 'dark' || saved === 'system' ? saved : 'system';
  });
  const [connectionStatus, setConnectionStatus] = useState<ConnectionStatus>('connecting');
  // 键盘快捷键：设置页可改，存 localStorage，文件页读同一份映射
  const [shortcuts, setShortcuts] = useState<ShortcutMap>(() => loadShortcuts());
  const handleChangeShortcuts = (next: ShortcutMap) => {
    saveShortcuts(next);
    setShortcuts(next);
  };

  // 输入框/下拉的「选中 / 编辑」两段式状态。
  //
  // 输入：指针按下（点进控件）与焦点离开
  // 输出：editingControl 指向当前正在编辑的控件，未编辑时为 null
  // 预期：方向键选中的控件只是「选上」，不接收文字也不改选项；
  //       点击或回车才开始编辑，Esc 退出编辑后再按一次才离开该控件
  const editingControl = useRef<HTMLElement | null>(null);
  useEffect(() => {
    const onPointerDown = (e: PointerEvent) => {
      const hit =
        (e.target as HTMLElement | null)?.closest<HTMLElement>('input, textarea, select') ?? null;
      editingControl.current = isTextField(hit) || hit?.tagName === 'SELECT' ? hit : null;
      markEditable(hit, editingControl.current !== null);
    };
    // 键盘走到输入框时先只读：字符要等 Enter 才进来，读屏也能据此播报
    const onFocusIn = (e: FocusEvent) => {
      const el = e.target as Element | null;
      if (!isTextField(el as HTMLElement | null)) return;
      if (el?.closest('dialog[open]')) return;
      markEditable(el, el === editingControl.current);
    };
    const onFocusOut = (e: FocusEvent) => {
      if (e.target === editingControl.current) editingControl.current = null;
      markEditable(e.target as Element | null, false);
    };
    document.addEventListener('pointerdown', onPointerDown, true);
    document.addEventListener('focusin', onFocusIn, true);
    document.addEventListener('focusout', onFocusOut, true);
    return () => {
      document.removeEventListener('pointerdown', onPointerDown, true);
      document.removeEventListener('focusin', onFocusIn, true);
      document.removeEventListener('focusout', onFocusOut, true);
    };
  }, []);

  // 全局键盘：方向键移动光标，Enter 确认/进入编辑，Esc 逐层退出。
  //
  // 输入：document keydown（冒泡阶段，让页面内控件先处理）
  // 输出：无；改焦点、滚动或切换「选中/编辑」状态
  // 预期：输入框分两段——光标停在框上时方向键仍能走开，Enter 才进入编辑；
  //       编辑中 Esc 退出编辑，再把键交还原生（光标移动、输入、回车提交）；
  //       已打开弹窗里完全不管，原生 Tab 与输入照旧
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.defaultPrevented) return;
      if (e.ctrlKey || e.altKey || e.metaKey) return;
      const target = e.target as HTMLElement | null;
      const tag = target?.tagName ?? '';

      const dir = matchesShortcut(e, shortcuts.moveUp)
        ? 'up'
        : matchesShortcut(e, shortcuts.moveDown)
          ? 'down'
          : matchesShortcut(e, shortcuts.moveLeft)
            ? 'left'
            : matchesShortcut(e, shortcuts.moveRight)
              ? 'right'
              : null;

      if (document.querySelector('dialog[open]')) return;

      if (isTextField(target)) {
        if (!target) return;
        // 只有回车/点击进入过的输入框才接受文字：否则方向键一落到搜索框，
        // 整片键盘就被它吃掉（光标在框内跳，走不出去）
        if (target !== editingControl.current) {
          if (dir) {
            if (moveFocusDirection(dir)) e.preventDefault();
            return;
          }
          if (matchesShortcut(e, shortcuts.confirm)) {
            e.preventDefault();
            editingControl.current = target;
            markEditable(target, true);
            return;
          }
          if (matchesShortcut(e, shortcuts.back)) {
            if (!focusAdjacent(target, 1)) focusPrimaryNav();
            return;
          }
          // 未进入编辑前，普通字符不许落进输入框
          e.preventDefault();
          return;
        }
        if (matchesShortcut(e, shortcuts.back)) {
          // 退出编辑但保留焦点：再按一次 Esc 才离开这个框
          editingControl.current = null;
          markEditable(target, false);
        }
        return;
      }

      if (tag === 'SELECT') {
        if (!target) return;
        // 与文本框同一套两段式：光标落在下拉上时方向键继续走，
        // Enter 才进入「改选项」，否则上下键会把焦点永远锁在这一项里
        if (target !== editingControl.current) {
          if (dir) {
            if (moveFocusDirection(dir)) e.preventDefault();
            return;
          }
          if (matchesShortcut(e, shortcuts.confirm)) {
            e.preventDefault();
            editingControl.current = target;
            return;
          }
          if (matchesShortcut(e, shortcuts.back)) {
            if (!focusAdjacent(target, 1)) focusPrimaryNav();
            return;
          }
          return;
        }
        if (matchesShortcut(e, shortcuts.back)) {
          // 退出改选项，焦点留在下拉上，再按一次 Esc 才离开
          editingControl.current = null;
        }
        return;
      }

      if (dir) {
        if (moveFocusDirection(dir)) e.preventDefault();
        return;
      }
      if (matchesShortcut(e, shortcuts.prevPage) && scrollActiveByPage(-1)) {
        e.preventDefault();
        return;
      }
      if (matchesShortcut(e, shortcuts.nextPage) && scrollActiveByPage(1)) {
        e.preventDefault();
        return;
      }
      if (matchesShortcut(e, shortcuts.home) && scrollActiveToEdge('start')) {
        e.preventDefault();
        return;
      }
      if (matchesShortcut(e, shortcuts.end) && scrollActiveToEdge('end')) {
        e.preventDefault();
        return;
      }
      if (matchesShortcut(e, shortcuts.confirm) && activateActive()) {
        e.preventDefault();
        return;
      }
      if (matchesShortcut(e, shortcuts.back)) {
        // 页面自己的 Esc 处理在这之后才跑，用微任务确认没人接再回导航
        queueMicrotask(() => {
          if (e.defaultPrevented) return;
          focusPrimaryNav();
        });
      }
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [shortcuts]);

  const [volumes, setVolumes] = useState<VolumeInfo[]>([]);
  const [files, setFiles] = useState<FileItem[]>([]);
  const [filesLoading, setFilesLoading] = useState(true);
  // filesLoadedRef 记录「首屏文件是否已拿到」。
  //
  // 输入：无
  // 输出：无
  // 预期：只有首次加载为 false，之后的事件驱动刷新都为 true；
  //       骨架屏只服务首次加载，刷新时不再插进来闪一下
  const filesLoadedRef = useRef(false);
  const [uploadTasks, setUploadTasks] = useState<TransferTask[]>([]);
  // stagedUploads 是「已选好、还没开始传」的清单：确认前不发任何请求
  const [stagedUploads, setStagedUploads] = useState<StagedUpload[]>([]);
  // textDraft 非空时打开「新建文本」弹窗，initial 可来自剪贴板的纯文本
  const [textDraft, setTextDraft] = useState<{ initial: string } | null>(null);
  const [serverTransfers, setServerTransfers] = useState<TransferTask[]>([]);
  const [serverQueue, setServerQueue] = useState<TransferTask[]>([]);
  const [settings, setSettings] = useState<AppSettings>(EMPTY_SETTINGS);
  // 下载目录列表由服务端扫描磁盘得到，页面只负责展示与刷新
  const [downloads, setDownloads] = useState<DownloadItem[]>([]);
  const [downloadTasks, setDownloadTasks] = useState<DownloadTask[]>([]);
  const [downloadsDir, setDownloadsDir] = useState('');

  const [activeVolumeId, setActiveVolumeId] = useState<string>(() =>
    urlParam('vol') || loadFromStorage<string>(STORAGE_KEYS.activeVol, '')
  );
  // 用户显式选过的卷（URL 参数 / 上次会话 / 点击）：卷是异步挂载的，
  // 首屏可能只回来一部分，偏好卷要等它出现而不是被首屏第一项顶掉
  const preferredVolume = useRef<string>(urlParam('vol') || loadFromStorage<string>(STORAGE_KEYS.activeVol, ''));
  const selectVolume = useCallback((id: string) => {
    preferredVolume.current = id;
    setActiveVolumeId(id);
  }, []);
  const [currentFolderId, setCurrentFolderId] = useState<string | null>(null);

  const [previewFile, setPreviewFile] = useState<FileItem | null>(null);
  const [localPreview, setLocalPreview] = useState<DownloadItem | null>(null);
  const [toasts, setToasts] = useState<ToastMessage[]>([]);
  // 常驻错误：后台拉取失败、实时连接断开这类「用户必须知道」的问题。
  // 用横幅而不是 toast，toast 几秒就消失，断网后用户会以为一切正常。
  // key 用于同一来源重复失败只保留一条，恢复后按 key 清除。
  const [problems, setProblems] = useState<Record<string, string>>({});
  // 卷管理能力来自服务端：桌面壳可建/删卷，纯网页模式可能只读
  const [caps, setCaps] = useState<{
    createVolume: boolean;
    deleteVolume: boolean;
    attachVolume: boolean;
    listChannels: boolean;
    login: boolean;
    autostart: boolean;
    cover: boolean;
    rebuild: boolean;
  }>({
    createVolume: false,
    deleteVolume: false,
    attachVolume: false,
    listChannels: false,
    login: false,
    autostart: false,
    cover: false,
    rebuild: false,
  });

  const t: Translations = DICTIONARY[locale] ?? DICTIONARY.zh!;
  const localeMeta = getLocaleMeta(locale);
  const summary = computeOverviewSummary(volumes, files);
  const problemList = useMemo(() => Object.values(problems), [problems]);
  const viewSpring = useInterruptibleViewSpring(activeTab);

  // uploadAbort 记录每个上传任务的取消句柄；uploadInputs 保留 File 以便暂停后重发
  const uploadAbort = useRef(new Map<string, AbortController>());
  // longOps 记录建卷/重建索这类长任务的取消句柄，键形如 create:<name>
  const longOps = useRef(new Map<string, AbortController>());
  // rel 保留浏览器给的相对路径（文件夹上传），重发时层级不能丢
  const uploadInputs = useRef(
    new Map<
      string,
      {
        file: File;
        volumeId: string;
        parentId: string | null;
        rel: string;
        task: TransferTask;
        resume?: UploadResume;
      }
    >()
  );
  // 客户端并发闸门：服务端分片会话有上限，回收协程还会清掉超时会话，
  // 一次性把整文件夹全发出去，后面的文件会撞 404「会话过期」
  const uploadQueue = useRef<string[]>([]);
  const activeUploads = useRef(0);

  // 速度采样：只展示真实测得的字节增量，不编造速率
  const uploadSpeed = useRef(new Map<string, { bytes: number; at: number }>());
  const downloadSpeed = useRef(new Map<string, { bytes: number; at: number }>());
  // 文案与语言用 ref 读取：后台回调不因切语言而重建连接或定时器
  const tRef = useRef(t);
  tRef.current = t;
  const localeRef = useRef(locale);
  localeRef.current = locale;
  // 连接状态跃迁回调：变成已连接时立即重拉文件与下载（由下方 effect 填充）
  const onConnectedRef = useRef<() => void>(() => {});
  // 上一次连接状态：reloadState 是唯一能观察到跃迁的位置
  const connRef = useRef<ConnectionStatus>('connecting');

  // 合并上传中、下载中与服务端队列任务。
  //
  // 输入：浏览器侧上传任务、服务端下载进度、服务端队列快照
  // 输出：一行的上传任务优先取服务端队列的真实字节数（浏览器→面板那一段是 localhost，
  //       秒到，真正耗时在队列里的 Telegram 上传）
  // 预期：同一个文件只出现一行；队列里没有对应行的任务照旧显示
  const transfers = useMemo(() => {
    const byKey = new Map<string, TransferTask>();
    for (const q of serverQueue) byKey.set(`${q.volumeId}:${q.fileName}`, q);
    const merged = uploadTasks.map((t) => {
      const key = `${t.volumeId}:${t.fileName}`;
      const q = byKey.get(key);
      if (!q) return t;
      byKey.delete(key);
      const size = q.size > 0 ? q.size : t.size;
      const done = q.state === 'completed' ? size : Math.max(t.transferredBytes, q.transferredBytes);
      const state: TransferTask['state'] =
        q.state === 'failed'
          ? 'failed'
          : q.state === 'completed'
            ? 'completed'
            : t.state === 'completed' || t.state === 'failed'
              ? 'queued'
              : t.state;
      return {
        ...t,
        size,
        transferredBytes: done,
        state,
        errorMessage: q.errorMessage ?? t.errorMessage,
      };
    });
    return [...merged, ...serverTransfers, ...byKey.values()];
  }, [uploadTasks, serverTransfers, serverQueue]);

  const cycleNextLocale = () => {
    const idx = LOCALE_OPTIONS.findIndex((o) => o.id === locale);
    const next = LOCALE_OPTIONS[(idx + 1) % LOCALE_OPTIONS.length]!;
    setLocale(next.id);
  };

  // pushToast 弹一条提示并返回 id；pending 类型不自动消失，
  // 由调用方在操作完成/失败后 dismissToast 撤掉再换结果 toast
  const pushToast = (
    text: string,
    type: 'success' | 'error' | 'pending' = 'success',
    action?: { label: string; onClick: () => void }
  ): string => {
    const id = `toast-${Date.now()}-${Math.random().toString(36).slice(2, 6)}`;
    setToasts((prev) => [...prev.slice(-3), { id, type, text, ...(action ? { action } : {}) }]);
    if (type !== 'pending') {
      window.setTimeout(() => {
        setToasts((prev) => prev.filter((item) => item.id !== id));
      }, 4500);
    }
    return id;
  };

  const dismissToast = (id: string) => {
    setToasts((prev) => prev.filter((item) => item.id !== id));
  };

  // setProblem 记录一条常驻错误；同 key 同文案不重复渲染。
  //
  // 输入：key 错误来源；text 给用户看的原因
  // 输出：无（写入 problems）
  // 预期：最多保留 4 条，按来源覆盖；恢复时由 clearProblem 移除
  const setProblem = useCallback((key: string, text: string) => {
    setProblems((prev) => {
      if (prev[key] === text) return prev;
      const next = { ...prev, [key]: text };
      const keys = Object.keys(next);
      if (keys.length > 4) {
        for (const k of keys.slice(0, keys.length - 4)) delete next[k];
      }
      return next;
    });
  }, []);

  // clearProblem 清除某个来源的错误；来源恢复正常时调用。
  const clearProblem = useCallback((key: string) => {
    setProblems((prev) => {
      if (!(key in prev)) return prev;
      const next = { ...prev };
      delete next[key];
      return next;
    });
  }, []);

  // errText 把未知异常转成可展示文案。
  const errText = (err: unknown): string =>
    err instanceof Error ? err.message : String(err);

  // 服务端状态转连接状态码
  const connOf = (st?: ApiState['status']): ConnectionStatus => {
    switch (st?.state_code) {
      case 'online':
        return 'connected';
      case 'reconnecting':
        return 'reconnecting';
      case 'auth_lost':
        return 'invalid_credentials';
      case 'connecting':
      case 'starting':
        return 'connecting';
      default:
        return 'connected';
    }
  };

  // sampleDownloads 把服务端进度转成任务，并按两次采样的字节差算真实速率
  const sampleDownloads = useCallback((items: ApiTransfer[]): TransferTask[] => {
    const now = performance.now();
    return items.map((t, i) => {
      const task = transferFromServer(t, i);
      const prev = downloadSpeed.current.get(task.id);
      if (prev && now > prev.at && task.transferredBytes >= prev.bytes) {
        task.speedBps = ((task.transferredBytes - prev.bytes) * 1000) / (now - prev.at);
      }
      downloadSpeed.current.set(task.id, { bytes: task.transferredBytes, at: now });
      return task;
    });
  }, []);

  // reloadState 只刷新卷摘要与传输进度（轻量，事件驱动）
  //
  // 输入：无
  // 输出：卷名列表；失败返回 nil
  // 预期：失败时写常驻横幅（后台失败不能静默），成功后清除该条；
  // 当前选中的卷被删除时回落到第一个卷
  const reloadState = useCallback(async (): Promise<string[] | null> => {
    try {
      const st = await getState();
      const names = st.volumes.map((v) => v.name);
      setVolumes(
        st.volumes.map((v) => ({
          id: v.name,
          name: v.name,
          title: v.title ?? '',
          cover: v.cover ?? '',
        }))
      );
      setActiveVolumeId((cur) => pickActiveVolume(preferredVolume.current, cur, names));
      // 非已连接 → 已连接的跃迁：断线/启动期间错过的变化立刻补拉，
      // 不等下一个推送事件（SSE 重连自带 ready，也会走这条路）
      const next = connOf(st.status);
      if (connRef.current !== 'connected' && next === 'connected') {
        onConnectedRef.current();
      }
      connRef.current = next;
      setConnectionStatus(next);
      setServerTransfers(sampleDownloads(st.status?.transfers ?? []));
      setServerQueue((st.status?.queue ?? []).map(queueFromServer));
      setCaps({
        createVolume: Boolean(st.caps?.createVolume),
        deleteVolume: Boolean(st.caps?.deleteVolume),
        attachVolume: Boolean(st.caps?.attachVolume),
        listChannels: Boolean(st.caps?.listChannels),
        login: Boolean(st.caps?.login),
        autostart: Boolean(st.caps?.autostart),
        cover: Boolean(st.caps?.cover),
        rebuild: Boolean(st.caps?.rebuild),
      });
      clearProblem('state');
      return names;
    } catch (err) {
      setProblem(
        'state',
        interpolate(tRef.current.errors.state, { message: errText(err) }, localeRef.current)
      );
      return null;
    }
  }, [sampleDownloads, setProblem, clearProblem]);

  // reloadFiles 拉取所有卷的完整条目（索引查询，不进 Telegram）
  const volumesRef = useRef<VolumeInfo[]>([]);
  useEffect(() => {
    volumesRef.current = volumes;
  }, [volumes]);

  const reloadFiles = useCallback(async (names?: string[]) => {
    const targets = names ?? volumesRef.current.map((v) => v.id);
    if (targets.length === 0) {
      setFilesLoading(false);
      return;
    }
    // 刷新不该重放骨架：骨架只在首次加载时占位，
    // 空卷每次收到推送都重插骨架会变成不停闪的条形占位
    if (!filesLoadedRef.current) setFilesLoading(true);
    const results = await Promise.all(
      targets.map(async (name): Promise<ApiFile[] | null> => {
        try {
          const list = await listFiles(name, true);
          clearProblem(`files:${name}`);
          return list;
        } catch (err) {
          // 单个卷读失败不能当空目录：横幅里点名是哪个卷、原因是什么
          setProblem(
            `files:${name}`,
            interpolate(
              tRef.current.errors.files,
              { volume: name, message: errText(err) },
              localeRef.current
            )
          );
          return null;
        }
      })
    );
    // 只替换本次真正读到的卷，其余卷保留旧条目：
    // 单卷刷新（新建/改名/删除后）若整体替换，空卷会让 files 变空，
    // 与全量刷新交替时界面就在骨架和列表之间闪
    const fresh = new Map<string, ApiFile[]>();
    const failed = new Set<string>();
    targets.forEach((name, i) => {
      const list = results[i];
      if (list) fresh.set(name, list);
      else failed.add(name);
    });
    const keepOld = (vol: string) => {
      if (fresh.has(vol)) return false;
      // 单卷刷新时其它卷不在本次范围，原样保留；
      // 全量刷新时只保留本次读取失败的卷，已删卷随之消失
      return names ? true : failed.has(vol);
    };
    setFiles((prev) => [
      ...prev.filter((f) => keepOld(f.volumeId)),
      ...Array.from(fresh.values()).flat().map(toFileItem),
    ]);
    filesLoadedRef.current = true;
    setFilesLoading(false);
  }, [setProblem, clearProblem]);

  const reloadSettings = useCallback(async () => {
    try {
      const s = await getSettings();
      // 面板只展示服务端给的真实生效值；前端不再自带任何默认数字
      setSettings((prev) => ({
        ...prev,
        schema: s.schema ?? [],
        values: s.values ?? {},
      }));
      clearProblem('settings');
    } catch (err) {
      setProblem(
        'settings',
        interpolate(tRef.current.errors.settings, { message: errText(err) }, localeRef.current)
      );
    }
  }, [setProblem, clearProblem]);

  // reloadDownloads 读取下载目录；失败时在横幅里说明原因，而不是留一个空列表
  const reloadDownloads = useCallback(async () => {
    try {
      const res = await getDownloads();
      setDownloads(res.files ?? []);
      setDownloadTasks(res.tasks ?? []);
      setDownloadsDir(res.dir ?? '');
      clearProblem('downloads');
    } catch (err) {
      setProblem(
        'downloads',
        interpolate(tRef.current.errors.settings, { message: errText(err) }, localeRef.current)
      );
    }
  }, [setProblem, clearProblem]);

  // 切到下载页时拉一次，避免在其它页面反复扫磁盘
  useEffect(() => {
    if (activeTab === 'downloads') void reloadDownloads();
  }, [activeTab, reloadDownloads]);

  // retryLoad 手动重试全部后台加载；失败会在各自回调里重新写回横幅
  const retryLoad = () => {
    setProblems({});
    void reloadState();
    void reloadFiles();
    void reloadSettings();
    void reloadDownloads();
  };

  // 事件驱动：服务端有变化时才推送，前端收到后拉最新快照（取代定时轮询）
  useEffect(() => {
    // 状态与文件走「首事件立即拉 + 窗口内合并」节流：
    // 单次操作（删/改名/建目录）推送后立刻生效，进度类突发仍合并不刷屏
    let stateTimer = 0;
    let stateTrailing = false;
    const scheduleState = () => {
      if (stateTimer !== 0) {
        stateTrailing = true;
        return;
      }
      void reloadState();
      stateTimer = window.setTimeout(() => {
        stateTimer = 0;
        if (stateTrailing) {
          stateTrailing = false;
          scheduleState();
        }
      }, 400);
    };
    let filesTimer = 0;
    let filesTrailing = false;
    const scheduleFiles = () => {
      if (filesTimer !== 0) {
        filesTrailing = true;
        return;
      }
      void reloadFiles();
      filesTimer = window.setTimeout(() => {
        filesTimer = 0;
        if (filesTrailing) {
          filesTrailing = false;
          scheduleFiles();
        }
      }, 1000);
    };
    // 下载进度也要跟着推送走：只读本地目录，代价很低
    let dlTimer = 0;
    let dlTrailing = false;
    const scheduleDownloads = () => {
      if (dlTimer !== 0) {
        dlTrailing = true;
        return;
      }
      void reloadDownloads();
      dlTimer = window.setTimeout(() => {
        dlTimer = 0;
        if (dlTrailing) {
          dlTrailing = false;
          scheduleDownloads();
        }
      }, 400);
    };
    // 连接从「连接中/重连中」跳到「已连接」时立即补拉：断线期间错过的变化不再等推送
    onConnectedRef.current = () => {
      scheduleFiles();
      scheduleDownloads();
    };

    const onStatus = () => {
      clearProblem('events');
      scheduleState();
      scheduleFiles();
      scheduleDownloads();
    };
    // 进度事件只动字节数：文件树没变，重拉整棵树在大卷上纯属白烧
    const onProgress = () => {
      clearProblem('events');
      scheduleState();
      scheduleDownloads();
    };
    const onConfig = () => {
      void reloadSettings();
      scheduleState();
      scheduleFiles();
    };

    // 桌面壳与网页版共用 subscribePush：组件里不再出现传输通道判断
    const unsubscribe = subscribePush(
      { status: onStatus, progress: onProgress, config: onConfig },
      {
        onConnect: onStatus,
        // 断线时浏览器会一直静默重连；不提示的话用户会以为数据是实时的
        onError: () => setProblem('events', tRef.current.errors.connection),
      }
    );

    // 先订阅再首拉：订阅前发生的变化由这次拉取覆盖，订阅后的变化由事件刷新
    const bootstrap = async () => {
      const names = await reloadState();
      if (names && names.length > 0) await reloadFiles(names);
      await reloadSettings();
    };
    void bootstrap();

    // 窗口重新可见 / 重新聚焦：宿主的 webview 可能晚于挂载才显示，补一次状态
    const onVisible = () => {
      if (document.visibilityState !== 'visible') return;
      scheduleState();
      scheduleFiles();
    };
    window.addEventListener('focus', onVisible);
    document.addEventListener('visibilitychange', onVisible);

    return () => {
      window.clearTimeout(stateTimer);
      window.clearTimeout(filesTimer);
      window.clearTimeout(dlTimer);
      window.removeEventListener('focus', onVisible);
      document.removeEventListener('visibilitychange', onVisible);
      unsubscribe();
    };
  }, [reloadState, reloadFiles, reloadSettings]);

  // 主题
  //
  // 输入：theme 三档（浅色 / 深色 / 跟随系统）
  // 输出：<html> 上的 dark 类
  // 预期：跟随系统时监听 prefers-color-scheme，系统切深浅后立即生效，
  //       不需要刷新页面；偏好本身写进 localStorage 供下次启动复用
  useEffect(() => {
    const root = document.documentElement;
    window.localStorage.setItem(STORAGE_KEYS.theme, JSON.stringify(theme));
    if (theme !== 'system') {
      root.classList.toggle('dark', theme === 'dark');
      return;
    }
    const media = window.matchMedia('(prefers-color-scheme: dark)');
    const apply = () => root.classList.toggle('dark', media.matches);
    apply();
    media.addEventListener('change', apply);
    return () => media.removeEventListener('change', apply);
  }, [theme]);

  useEffect(() => {
    window.localStorage.setItem(STORAGE_KEYS.tab, JSON.stringify(activeTab));
  }, [activeTab]);

  useEffect(() => {
    window.localStorage.setItem(STORAGE_KEYS.locale, JSON.stringify(locale));
    const meta = getLocaleMeta(locale);
    document.documentElement.lang = meta.htmlLang;
    document.documentElement.dir = meta.dir;
    document.title = DICTIONARY[locale]?.pageTitle || 'mytgfs';
    if (i18next.language !== locale) {
      i18next.changeLanguage(locale).catch(() => {});
    }
  }, [locale]);

  useEffect(() => {
    if (activeVolumeId) window.localStorage.setItem(STORAGE_KEYS.activeVol, JSON.stringify(activeVolumeId));
  }, [activeVolumeId]);

  // 记住每个卷上次停留的目录：刷新后回到原处；目录已被删则回根。
  // restoredFolder 保证只在首次拿到文件列表时恢复一次，之后用户操作优先。
  const restoredFolder = useRef<string | null>(null);
  useEffect(() => {
    if (!activeVolumeId || files.length === 0) return;
    if (restoredFolder.current === activeVolumeId) return;
    restoredFolder.current = activeVolumeId;
    const saved = loadFromStorage<string | null>(`mytgfs_folder_${activeVolumeId}_v1`, null);
    if (saved && files.some((f) => f.id === saved && f.isDir && f.volumeId === activeVolumeId)) {
      setCurrentFolderId(saved);
    }
  }, [activeVolumeId, files]);

  useEffect(() => {
    // 恢复完成前不要用初始的 null 覆盖已保存的目录
    if (!activeVolumeId || restoredFolder.current !== activeVolumeId) return;
    try {
      window.localStorage.setItem(
        `mytgfs_folder_${activeVolumeId}_v1`,
        JSON.stringify(currentFolderId)
      );
    } catch {
      // 隐私模式禁用存储时忽略
    }
  }, [activeVolumeId, currentFolderId]);

  // 单文件上传：更新任务进度，成功后刷新索引
  // 断点保存在 uploadInputs 里：暂停后继续从已传字节接着发
  const runUpload = async (
    task: TransferTask,
    file: File,
    volumeId: string,
    parentId: string | null,
    rel: string
  ) => {
    const ctl = new AbortController();
    uploadAbort.current.set(task.id, ctl);
    const resume = uploadInputs.current.get(task.id)?.resume;
    setUploadTasks((prev) =>
      prev.map((tr) => (tr.id === task.id ? { ...tr, state: 'transferring' } : tr))
    );
    try {
      const res = await uploadFile(
        volumeId,
        parentId || '/',
        rel,
        file,
        (ratio) => {
          const bytes = Math.round(task.size * ratio);
          const now = performance.now();
          const prev = uploadSpeed.current.get(task.id);
          const bps =
            prev && now > prev.at ? Math.max(0, ((bytes - prev.bytes) * 1000) / (now - prev.at)) : 0;
          uploadSpeed.current.set(task.id, { bytes, at: now });
          setUploadTasks((prevTasks) =>
            prevTasks.map((tr) =>
              tr.id === task.id
                ? {
                    ...tr,
                    transferredBytes: bytes,
                    // 间隔太短时沿用上次速率，避免数字乱跳
                    speedBps: bps > 0 ? bps : tr.speedBps,
                  }
                : tr
            )
          );
        },
        ctl.signal,
        resume
      );
      // 视频补一张封面，失败不影响上传结果
      if (file.type.startsWith('video/')) {
        try {
          const poster = await makeVideoPoster(file);
          if (poster) await uploadPoster(volumeId, res.path, poster);
        } catch {
          // 浏览器不支持解码该视频时保持 Telegram 缩略图或占位图
        }
      }
      setUploadTasks((prev) =>
        prev.map((tr) =>
          tr.id === task.id
            ? { ...tr, state: 'completed', transferredBytes: tr.size, speedBps: 0 }
            : tr
        )
      );
      uploadAbort.current.delete(task.id);
      // 传完就不再需要 File 与速率采样：留着会把整份文件钉在内存里
      uploadInputs.current.delete(task.id);
      uploadSpeed.current.delete(task.id);
      await reloadFiles();
      await reloadState();
    } catch (err) {
      // 暂停时保留服务端会话与已传字节，继续时从断点续传
      const paused = err instanceof UploadPaused;
      const aborted = err instanceof Error && err.message === 'aborted';
      const stopped = paused || aborted;
      if (paused) {
        const input = uploadInputs.current.get(task.id);
        if (input) {
          input.resume = { id: err.id, offset: err.offset };
        } else {
          // 取消上传会先删掉 uploadInputs 再 abort，这里据此丢弃服务端临时文件
          void discardUpload(err.id);
        }
      }
      setUploadTasks((prev) =>
        prev.map((tr) =>
          tr.id === task.id
            ? {
                ...tr,
                state: stopped ? 'paused' : 'failed',
                speedBps: 0,
                errorMessage: stopped ? undefined : err instanceof Error ? err.message : String(err),
              }
            : tr
        )
      );
      uploadAbort.current.delete(task.id);
      if (!stopped) pushToast(err instanceof Error ? err.message : String(err), 'error');
    } finally {
      // 无论成功、失败还是暂停，都要让出并发名额，后面的任务才能开始
      activeUploads.current = Math.max(0, activeUploads.current - 1);
      pumpUploads();
    }
  };

  // pumpUploads 按并发上限启动排队中的上传。
  //
  // 输入：无（读 uploadQueue / uploadInputs）
  // 输出：无
  // 预期：最多 MAX_PARALLEL_UPLOADS 路同时在跑；名额释放后自动续跑下一个
  const pumpUploads = () => {
    while (activeUploads.current < MAX_PARALLEL_UPLOADS) {
      const id = uploadQueue.current.shift();
      if (!id) return;
      const input = uploadInputs.current.get(id);
      if (!input) continue;
      activeUploads.current += 1;
      void runUpload(input.task, input.file, input.volumeId, input.parentId, input.rel);
    }
  };

  // handleUploadSources 把一批来源放进「上传前清单」，不立刻开始传。
  //
  // 输入：sources 文件与相对路径；volumeId 目标卷；parentId 目标目录
  // 输出：无
  // 预期：
  //   - 预检不通过的文件单独提示并跳过，其余照常进清单
  //   - 确认前不发 /api/upload/init，用户移除的文件不会留下任何痕迹
  //   - 清单已打开时继续追加（再拖一批/再粘一张），不重开弹窗
  const handleUploadSources = (
    sources: UploadSource[],
    volumeId: string,
    parentId: string | null
  ) => {
    // 上传前预检：单个文件不合格只跳过它，其余照常进清单，
    // 不因为一个超大文件把整批都丢掉（跳过哪些要明说）
    const accepted = sources.filter(({ file }) => {
      const verdict = validateAndPlanUpload(file.name, file.size);
      if (verdict.valid) return true;
      pushToast(
        verdict.errorCode === 'FILE_TOO_LARGE'
          ? interpolate(tRef.current.files.uploadTooLarge, { name: file.name }, localeRef.current)
          : tRef.current.files.uploadEmptyName,
        'error'
      );
      return false;
    });
    if (accepted.length === 0) return;
    setStagedUploads((prev) => [...prev, ...stageSources(accepted, volumeId, parentId)]);
  };

  // handleUploadFiles 是 input[file] / input[webkitdirectory] 的入口。
  //
  // 输入：浏览器给的文件列表
  // 输出：无
  // 预期：目录选择时把 webkitRelativePath 原样带进清单
  const handleUploadFiles = (fileList: FileList, volumeId: string, parentId: string | null) => {
    handleUploadSources(sourcesFromFiles(fileList), volumeId, parentId);
  };

  // handleConfirmUpload 把确认后的草稿转成真正的上传任务。
  //
  // 输入：kept 清单里留下的草稿项
  // 输出：无
  // 预期：先清空清单再入队；与旧实现共用 uploadInputs / uploadQueue，
  //       并发上限、批次号与断点续传行为完全不变
  const handleConfirmUpload = (kept: StagedUpload[]) => {
    setStagedUploads([]);
    if (kept.length === 0) return;
    // 同一次确认的文件共享批次号，概览页据此显示整批总进度
    const batchId = `batch-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`;
    const plan = toTransferTasks(kept, batchId);
    kept.forEach((item, index) => {
      const task = plan[index]!;
      uploadInputs.current.set(task.id, {
        file: item.file,
        volumeId: item.volumeId,
        parentId: item.parentId,
        rel: item.rel,
        task,
      });
    });
    // 最新的任务排在最上面，与操作队列显示顺序一致
    setUploadTasks((prev) => [...plan, ...prev]);
    pushToast(
      interpolate(t.files.uploadStartedToast, { name: plan[0]?.fileName ?? '' }, locale),
      'success'
    );
    uploadQueue.current.push(...plan.map((task) => task.id));
    pumpUploads();
  };

  // handleCancelUploadPlan 放弃整批草稿：清单清空，服务端什么都不知道。
  const handleCancelUploadPlan = () => setStagedUploads([]);

  // handleTextFileSave 把新建文本包成普通 File，走同一条上传清单。
  //
  // 输入：name 文件名；text 正文
  // 输出：无
  // 预期：正文为空时按钮已被弹窗拦住，后端不会收到 size=0 的会话
  const handleTextFileSave = (name: string, text: string) => {
    setTextDraft(null);
    const file = new File([text], name, { type: 'text/plain;charset=utf-8' });
    handleUploadSources([{ file, rel: '' }], activeVolumeId, currentFolderId);
  };

  // 文件页的粘贴入口：文件进上传清单，纯文本开「新建文本」预填。
  // 焦点在输入控件里时放行原生粘贴，不抢用户正在输入的内容。
  useEffect(() => {
    if (activeTab !== 'files') return;
    const onPaste = (e: ClipboardEvent) => {
      if (isEditableTarget(e.target)) return;
      const { files, text } = extractPasteContent(e.clipboardData);
      if (files.length === 0 && !text) return;
      e.preventDefault();
      if (files.length > 0) {
        handleUploadSources(files, activeVolumeId, currentFolderId);
        return;
      }
      setTextDraft({ initial: text });
    };
    document.addEventListener('paste', onPaste);
    return () => document.removeEventListener('paste', onPaste);
  }, [activeTab, activeVolumeId, currentFolderId]);

  const handleToggleTransferPause = (taskId: string) => {
    const task = uploadTasks.find((tr) => tr.id === taskId);
    if (!task) return;
    if (task.state === 'transferring') {
      uploadAbort.current.get(taskId)?.abort();
      return;
    }
    if (task.state === 'queued') {
      // 还没开始的任务直接出队，不占并发名额
      uploadQueue.current = uploadQueue.current.filter((id) => id !== taskId);
      setUploadTasks((prev) =>
        prev.map((tr) => (tr.id === taskId ? { ...tr, state: 'paused' } : tr))
      );
      return;
    }
    if (task.state === 'paused') {
      if (!uploadInputs.current.get(taskId)) return;
      setUploadTasks((prev) =>
        prev.map((tr) => (tr.id === taskId ? { ...tr, state: 'queued' } : tr))
      );
      uploadQueue.current.push(taskId);
      pumpUploads();
    }
  };

  // handleRetryTransfer 重试失败的上传：重新排队，不沿用可能已失效的会话
  const handleRetryTransfer = (taskId: string) => {
    const input = uploadInputs.current.get(taskId);
    if (!input) return;
    uploadInputs.current.set(taskId, { ...input, resume: undefined });
    setUploadTasks((prev) =>
      prev.map((tr) =>
        tr.id === taskId ? { ...tr, state: 'queued', errorMessage: undefined, speedBps: 0 } : tr
      )
    );
    uploadQueue.current.push(taskId);
    pumpUploads();
  };

  const handleCancelTransfer = (taskId: string) => {
    uploadAbort.current.get(taskId)?.abort();
    uploadAbort.current.delete(taskId);
    uploadInputs.current.delete(taskId);
    uploadQueue.current = uploadQueue.current.filter((id) => id !== taskId);
    setUploadTasks((prev) => prev.filter((tr) => tr.id !== taskId));
  };

  const handleClearCompletedTransfers = () => {
    // 服务端持久化表才是队列的真实来源：只清本地数组等于假按钮
    clearQueue()
      .then(() => {
        const dropped = uploadTasks.filter(
          (tr) => tr.state === 'completed' || tr.state === 'failed'
        );
        for (const tr of dropped) {
          // 清记录同时放掉 File 引用，否则重试入口没了、内存却还占着
          uploadInputs.current.delete(tr.id);
          uploadSpeed.current.delete(tr.id);
        }
        setUploadTasks((prev) => prev.filter((tr) => tr.state !== 'completed' && tr.state !== 'failed'));
        pushToast(t.overview.queueClearedToast, 'success');
      })
      .catch((err) => pushToast(err instanceof Error ? err.message : String(err), 'error'));
  };

  const handleCreateFolder = (volumeId: string, parentId: string | null, name: string) => {
    mkdir(volumeId, parentId || '/', name)
      .then(() => reloadFiles([volumeId]))
      .then(() => pushToast(interpolate(t.files.folderCreatedToast, { name }, locale), 'success'))
      .catch((err) => pushToast(err instanceof Error ? err.message : String(err), 'error'));
  };

  // 改名/移动：同一父目录只改末段，跨目录用目标目录 + 原名
  const handleRenameMoveItem = (itemId: string, newName: string, targetParentId: string | null) => {
    const item = files.find((f) => f.id === itemId);
    // 列表已经刷新掉这一行时不能静默返回：用户点了保存却什么都不发生等于假按钮
    if (!item) {
      void reloadFiles();
      pushToast(t.files.itemMissingToast, 'error');
      return;
    }
    const targetDir = targetParentId || '/';
    const dest = targetDir === '/' ? `/${newName}` : `${targetDir}/${newName}`;
    moveItems(item.volumeId, [itemId], targetDir, dest)
      .then(() => reloadFiles([item.volumeId]))
      .then(() => pushToast(t.files.itemMovedRenamedToast, 'success'))
      .catch((err) => pushToast(err instanceof Error ? err.message : String(err), 'error'));
  };

  const handleBatchMoveItems = (itemIds: string[], targetParentId: string | null) => {
    const vol = files.find((f) => f.id === itemIds[0])?.volumeId;
    if (!vol || itemIds.length === 0) {
      void reloadFiles();
      pushToast(t.files.itemMissingToast, 'error');
      return;
    }
    moveItems(vol, itemIds, targetParentId || '/')
      .then(() => reloadFiles([vol]))
      .then(() => pushToast(t.files.itemMovedRenamedToast, 'success'))
      .catch((err) => pushToast(err instanceof Error ? err.message : String(err), 'error'));
  };

  const handleDeleteItems = (itemIds: string[]) => {
    const vol = files.find((f) => f.id === itemIds[0])?.volumeId;
    if (!vol || itemIds.length === 0) {
      void reloadFiles();
      pushToast(t.files.itemMissingToast, 'error');
      return;
    }
    removeItems(vol, itemIds)
      .then(() => reloadFiles([vol]))
      .then(() => pushToast(t.files.deletedToast, 'success'))
      .catch((err) => pushToast(err instanceof Error ? err.message : String(err), 'error'));
  };

  // handleDownloadFile 启动后台下载：立刻反馈，进度由事件推送
  const handleDownloadFile = (file: FileItem) => {
    startDownload(file.volumeId, file.id)
      .then((res) => {
        setDownloadTasks((prev) => [...prev.filter((t) => t.id !== res.task.id), res.task]);
        pushToast(interpolate(t.downloads.startedToast, { name: file.name }, locale), 'success');
        return reloadDownloads();
      })
      .catch((err) => pushToast(err instanceof Error ? err.message : String(err), 'error'));
  };

  // handleDownloadTask 暂停/继续/取消单个下载任务
  const handleDownloadTask = (id: string, action: 'pause' | 'resume' | 'cancel') => {
    const call =
      action === 'pause' ? pauseDownload(id) : action === 'resume' ? resumeDownload(id) : cancelDownload(id);
    call
      .then(() => reloadDownloads())
      .catch((err) => pushToast(err instanceof Error ? err.message : String(err), 'error'));
  };

  // handleRemoveDownload 只删本地副本，云端文件不受影响
  const handleRemoveDownload = (name: string) => {
    removeDownload(name)
      .then(() => reloadDownloads())
      .then(() => {
        pushToast(t.downloads.removedToast, 'success');
      })
      .catch((err) => pushToast(err instanceof Error ? err.message : String(err), 'error'));
  };

  // handleOpenDownloads 用系统文件管理器打开下载目录
  const handleOpenDownloads = () => {
    openDownloads()
      .then(() => undefined)
      .catch((err) => {
        const msg = err instanceof Error ? err.message : String(err);
        pushToast(`${t.downloads.openFailedToast}：${msg}`, 'error');
      });
  };

  // downloadedPreview 把本地下载文件包成 FileItem，复用同一个预览弹窗
  // 媒体源由 streamOverride 指向 /api/downloads/file，不经过卷索引
  const downloadedPreview = useMemo<FileItem | null>(() => {
    if (!localPreview) return null;
    const category = inferFileCategory(localPreview.name, false);
    return {
      id: `download:${localPreview.name}`,
      volumeId: '',
      parentId: null,
      name: localPreview.name,
      isDir: false,
      size: localPreview.size,
      updatedAt: localPreview.mod,
      category,
      previewUrl: category === 'image' ? localFileURL(localPreview.name) : undefined,
    };
  }, [localPreview]);

  // handlePreviewDownloaded 打开本地文件预览；文件已被删则只刷新列表
  const handlePreviewDownloaded = (name: string) => {
    const item = downloads.find((f) => f.name === name);
    if (item) setLocalPreview(item);
  };

  // handleOpenDownloaded 交给系统默认程序打开
  const handleOpenDownloaded = (name: string) =>
    openDownloadedFile(name)
      .then(() => undefined)
      .catch((err) => pushToast(err instanceof Error ? err.message : String(err), 'error'));

  // handleSaveSettings 只提交卡片里的键，成功后本地合并，不再整页重拉
  const handleSaveSettings = (values: Record<string, string>) => {
    return saveSettings(values).then(() => {
      setSettings((prev) => ({
        ...prev,
        values: { ...prev.values, ...values },
      }));
    });
  };


  // handleCreateVolume 在 Telegram 侧建频道并写入配置，成功后刷新卷列表。
  // 建频道要十几秒：点击确认立刻弹「正在创建」并带取消按钮；
  // 取消会断开请求，服务端随即把刚建出来的空频道删掉
  const handleCreateVolume = (name: string, title: string) => {
    const ctrl = new AbortController();
    const key = `create:${name}`;
    longOps.current.set(key, ctrl);
    const pending = pushToast(interpolate(t.volume.creating, { name }, locale), 'pending', {
      label: t.files.cancel,
      onClick: () => ctrl.abort(),
    });
    createVolume(name, title, ctrl.signal)
      .then(() => Promise.all([reloadState(), reloadFiles()]))
      .then(() => {
        dismissToast(pending);
        pushToast(interpolate(t.volume.createSuccess, { name }, locale), 'success');
      })
      .catch(async (err: unknown) => {
        dismissToast(pending);
        if (ctrl.signal.aborted) {
          // 断开请求后服务端可能已经把卷建完并写进配置了：
          // 以卷列表为准，避免「提示已取消」但卷其实出现
          const names = await reloadState();
          if (names?.includes(name)) {
            if (names.length > 0) await reloadFiles(names);
            pushToast(interpolate(t.volume.createSuccess, { name }, locale), 'success');
          } else {
            pushToast(interpolate(t.volume.createCancelled, { name }, locale), 'success');
          }
          return;
        }
        pushToast(errText(err), 'error');
      })
      .finally(() => longOps.current.delete(key));
  };

  // handleAttachVolume 挂载已有频道：名称 + chat_id，后端只读校验后写配置
  const handleAttachVolume = (name: string, chatId: number) => {
    const pending = pushToast(interpolate(t.volume.attaching, { name }, locale), 'pending');
    attachVolume(name, chatId)
      .then(() => Promise.all([reloadState(), reloadFiles()]))
      .then(() => {
        dismissToast(pending);
        pushToast(interpolate(t.volume.attachSuccess, { name }, locale), 'success');
      })
      .catch((err) => {
        dismissToast(pending);
        pushToast(errText(err), 'error');
      });
  };

  // handleDeleteVolume 删卷不可撤销：删频道 → 移出配置 → 删索引
  const handleDeleteVolume = (name: string) => {
    const pending = pushToast(interpolate(t.volume.deleting, { name }, locale), 'pending');
    deleteVolume(name)
      .then(() => Promise.all([reloadState(), reloadFiles()]))
      .then(() => {
        dismissToast(pending);
        pushToast(interpolate(t.volume.deleteSuccess, { name }, locale), 'success');
      })
      .catch((err) => {
        dismissToast(pending);
        pushToast(errText(err), 'error');
      });
  };

  // handleSetCover 把原图写成频道头像；成功后重取状态拿到新版本号
  //
  // 输入：volume 卷名；file 用户选的原图
  // 输出：无（错误转 toast，不向弹窗抛）
  // 预期：失败时弹窗保持打开，用户可换一张再试
  const handleSetCover = async (volume: string, file: File): Promise<void> => {
    try {
      await setCover(volume, file);
      await reloadState();
      pushToast(t.volume.coverSaved, 'success');
    } catch (err) {
      pushToast(errText(err), 'error');
    }
  };

  // handleSetVolumeTitle 改频道标题；成功后重取状态刷新面板显示名
  //
  // 输入：volume 卷名；title 新频道名
  // 输出：无（错误转 toast，不向弹窗抛）
  // 预期：失败时弹窗保持打开，用户可继续改
  const handleSetVolumeTitle = async (volume: string, title: string): Promise<void> => {
    try {
      await setVolumeTitle(volume, title);
      await reloadState();
      pushToast(t.volume.titleSaved, 'success');
    } catch (err) {
      pushToast(errText(err), 'error');
    }
  };

  // handleRefresh 手动刷新状态、文件列表与下载任务。
  const handleRefresh = async (): Promise<void> => {
    const names = await reloadState();
    if (names && names.length > 0) await reloadFiles(names);
    await reloadDownloads();
  };

  // handleRebuildVolume 触发服务端按频道历史重建索引，完成后刷新当前卷。
  // 全量翻历史要几十秒：先弹「正在重建」并带取消按钮；取消后服务端在写本地之前就停了
  const handleRebuildVolume = async (volume: string): Promise<void> => {
    const ctrl = new AbortController();
    const key = `rebuild:${volume}`;
    longOps.current.set(key, ctrl);
    const pending = pushToast(
      interpolate(t.files.rebuilding, { name: volume }, locale),
      'pending',
      { label: t.files.cancel, onClick: () => ctrl.abort() }
    );
    try {
      const res = await rebuildVolume(volume, ctrl.signal);
      await reloadState();
      await reloadFiles([volume]);
      dismissToast(pending);
      pushToast(interpolate(t.files.rebuildDone, { count: res.files }, locale), 'success');
    } catch (err) {
      dismissToast(pending);
      if (ctrl.signal.aborted) {
        pushToast(interpolate(t.files.rebuildCancelled, { name: volume }, locale), 'success');
      } else {
        pushToast(errText(err), 'error');
      }
    } finally {
      longOps.current.delete(key);
    }
  };


  const [sidebarCollapsed, setSidebarCollapsed] = useState(false);

  const navItems: { id: NavTab; label: string; icon: React.ReactNode }[] = [
    { id: 'overview', label: t.nav.overview, icon: <LayoutDashboard className="w-4 h-4 shrink-0" /> },
    { id: 'files', label: t.nav.files, icon: <FolderTree className="w-4 h-4 shrink-0" /> },
    { id: 'settings', label: t.nav.settings, icon: <Sliders className="w-4 h-4 shrink-0" /> },
    { id: 'downloads', label: t.nav.downloads, icon: <HardDriveDownload className="w-4 h-4 shrink-0" /> },
  ];

  // 主题按钮：显示当前档位，点一下切到下一档，浅色 → 深色 → 跟随系统 → 浅色
  //
  // 输入：theme 当前档位
  // 输出：按钮显示的图标与文案取「当前档位」，themeNext 是点击后要切到的档位
  // 预期：文案反映现状，不会把「跟随系统」误读成当前已经在跟随系统
  const themeNext: ThemeMode = theme === 'light' ? 'dark' : theme === 'dark' ? 'system' : 'light';
  const ThemeIcon = theme === 'light' ? Sun : theme === 'dark' ? Moon : Monitor;
  const themeLabel =
    theme === 'light' ? t.header.themeLight : theme === 'dark' ? t.header.themeDark : t.header.themeSystem;
  const themeIconClass = `shrink-0 ${theme === 'light' ? 'text-amber-400' : 'text-zinc-700 dark:text-zinc-300'}`;

  return (
    <div
      dir={localeMeta.dir}
      className="min-h-screen flex flex-col bg-[#F5F5F7] dark:bg-[#090A0F] text-zinc-900 dark:text-zinc-100"
    >
      {/* 纯键盘用户按 Tab 第一下就跳到正文，跳过整排导航 */}
      <a
        href="#main"
        className="sr-only focus:not-sr-only focus:fixed focus:top-2 focus:start-2 focus:z-50 focus:px-3 focus:py-2 focus:rounded-lg focus:bg-white dark:focus:bg-zinc-900 focus:text-xs focus:font-medium focus:shadow-lg"
      >
        {t.header.skipToContent}
      </a>
      {/* Global Hidden File Input for Quick Upload */}
      <input
        ref={globalUploadRef}
        type="file"
        multiple
        onChange={(e) => {
          if (e.target.files && e.target.files.length > 0) {
            const targetVol = volumes.find((v) => v.id === activeVolumeId)?.id || volumes[0]?.id || '';
            if (!targetVol) {
              e.target.value = '';
              return;
            }
            handleUploadFiles(e.target.files, targetVol, currentFolderId);
            e.target.value = '';
          }
        }}
        className="hidden"
        aria-label={t.header.quickUpload}
      />

      <PhoneTopBar
        t={t}
        locale={locale}
        setLocale={setLocale}
        totalBytes={summary.totalBytes}
        setActiveTab={setActiveTab}
        themeNext={themeNext}
        themeLabel={themeLabel}
        themeIconClass={themeIconClass}
        ThemeIcon={ThemeIcon}
        setTheme={setTheme}
      />


      {/* Main Workspace Canvas: Collapsible iOS/iPadOS/macOS Spring Sidebar (md+) + Main Content Viewport */}
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
          downloadsCount={downloads.length}
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

        {/* Custom iOS / macOS Interactive Floating Window Scrollbar */}
        <WindowCustomScrollbar />

        {/* Main Content Viewport: Persistent, velocity-preserving iOS spring (zero unmount flash, 100% mid-flight interruptible) */}
        <main
          id="main"
          tabIndex={-1}
          className="flex-1 min-w-0 px-3.5 sm:px-6 lg:px-8 pt-4 sm:pt-7 pb-24 md:pb-8 max-w-5xl mx-auto w-full overflow-x-hidden outline-none"
        >
          <ProblemBanner
            messages={problemList}
            title={t.errors.title}
            retryLabel={t.errors.retry}
            dismissLabel={t.errors.dismiss}
            onRetry={retryLoad}
            onDismiss={() => setProblems({})}
          />
          <motion.div
            style={{
              y: viewSpring.y,
              scale: viewSpring.scale,
              opacity: viewSpring.opacity,
              transformOrigin: 'top center',
            }}
          >
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
                onClearCompletedTransfers={handleClearCompletedTransfers}
                onSetCover={caps.cover ? handleSetCover : undefined}
              onSetTitle={caps.cover ? handleSetVolumeTitle : undefined}
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
                onNewTextFile={() => setTextDraft({ initial: '' })}
                onNotifyError={(msg) => pushToast(msg, 'error')}
                onCreateFolder={handleCreateFolder}
                onRenameMoveItem={handleRenameMoveItem}
                onBatchMoveItems={handleBatchMoveItems}
                onMoveItems={handleBatchMoveItems}
                onDeleteItems={handleDeleteItems}
                onDownloadFile={handleDownloadFile}
                onPreviewFile={setPreviewFile}
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
                <SettingsView
                  t={t}
                  locale={locale}
                  settings={settings}
                  canAutostart={caps.autostart}
                  shortcuts={shortcuts}
                  onChangeShortcuts={handleChangeShortcuts}
                  onSaveSettings={handleSaveSettings}
                  onNotifyError={(msg) => pushToast(msg, 'error')}
                />
                {caps.login && <AccountCard t={t} onNotify={pushToast} />}
              </div>
            )}

            {activeTab === 'downloads' && (
              <DownloadsView
                t={t}
                locale={locale}
                dir={downloadsDir}
                files={downloads}
                tasks={downloadTasks}
                onRefresh={() => void reloadDownloads()}
                onOpenDir={handleOpenDownloads}
                onRemove={handleRemoveDownload}
                onTask={handleDownloadTask}
                onPreview={handlePreviewDownloaded}
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
        downloadsCount={downloads.length}
      />


      {/* File Details & Media Preview Modal */}
      <Suspense fallback={null}>
      <FilePreviewModal
        file={previewFile}
        allFiles={files}
        t={t}
        locale={locale}
        onClose={() => setPreviewFile(null)}
        onDownload={handleDownloadFile}
      />

      {/* 下载页本地预览：媒体源指向 /api/downloads/file，主按钮交给系统默认程序 */}
      <FilePreviewModal
        file={downloadedPreview}
        allFiles={[]}
        t={t}
        locale={locale}
        onClose={() => setLocalPreview(null)}
        onDownload={(f) => void handleOpenDownloaded(f.name)}
        streamOverride={localPreview ? localFileURL(localPreview.name) : undefined}
        actionLabel={t.downloads.openBtn}
      />
      </Suspense>

      {/* 上传前清单：确认之前不发任何请求，移除的文件直接作废 */}
      {stagedUploads.length > 0 && (
        <UploadPlanDialog
          t={t}
          locale={locale}
          items={stagedUploads}
          onConfirm={handleConfirmUpload}
          onCancel={handleCancelUploadPlan}
        />
      )}

      {/* 新建文本：保存后走普通上传清单 */}
      {textDraft !== null && (
        <TextFileDialog
          t={t}
          initialText={textDraft.initial}
          onSave={handleTextFileSave}
          onClose={() => setTextDraft(null)}
        />
      )}

      {/* iOS Dynamic Toast Notification Stack */}
      <ToastStack
        toasts={toasts}
        dismissLabel={t.a11y.dismissToast}
        onDismiss={(id) => setToasts((prev) => prev.filter((item) => item.id !== id))}
        onAction={(toast) => {
          toast.action?.onClick();
          setToasts((prev) => prev.filter((item) => item.id !== toast.id));
        }}
      />
    </div>
  );
}
