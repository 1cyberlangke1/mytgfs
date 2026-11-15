import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  AlertCircle,
  AlertTriangle,
  CheckCircle2,
  FolderTree,
  Globe,
  HardDrive,
  HardDriveDownload,
  LayoutDashboard,
  Loader2,
  Moon,
  Monitor,
  PanelLeftClose,
  PanelLeftOpen,
  Sliders,
  Sun,
  X,
} from 'lucide-react';
import { AnimatePresence, LayoutGroup, motion } from 'motion/react';
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
  formatBytes,
  inferFileCategory,
  validateAndPlanUpload,
  volumeDisplayName,
} from './utils/fsLogic';
import { loadShortcuts, matchesShortcut, saveShortcuts, type ShortcutMap } from './utils/shortcuts';
import {
  activateActive,
  focusPrimaryNav,
  moveFocusDirection,
  scrollActiveByPage,
  scrollActiveToEdge,
} from './utils/keynav';
import { runViewTransition } from './utils/viewTransition';
import { subscribePush } from './events';
import {
  DICTIONARY,
  LOCALE_OPTIONS,
  Translations,
  formatNumber,
  formatPercent,
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
  uploadFile,
  uploadPoster,
} from './api';
import { OverviewView } from './components/OverviewView';
import { FilesView } from './components/FilesView';
import { SettingsView } from './components/SettingsView';
import { AccountCard } from './components/AccountCard';
import { DownloadsView } from './components/DownloadsView';
import { FilePreviewModal } from './components/FilePreviewModal';
import { BrandEmblemSvg } from './components/VisualAssets';
import {
  Button,
  IOS_BOUNCE_SPRING,
  IOS_SOFT_SPRING,
  IOS_SPRING,
  Select,
  useInterruptibleViewSpring,
} from './components/ui';
import { CustomScrollArea, WindowCustomScrollbar } from './components/CustomScrollbar';
import { makeVideoPoster } from './utils/poster';

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

interface ToastMessage {
  id: string;
  type: 'success' | 'error' | 'pending';
  text: string;
  /** 可选操作按钮（如长任务的「取消」）；点一下执行并关掉这条提示 */
  action?: { label: string; onClick: () => void };
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

// queueFromServer 把服务端串行队列快照转成传输任务。
//
// 输入：q 队列项（volume/kind/name 由服务端给出）；index 序号
// 输出：按服务端 state 映射的传输任务（done→completed，failed 带原因）
// 预期：已完成的活儿必须显示「已完成」，不能一律当成排队中
function queueFromServer(q: ApiQueueItem, index: number): TransferTask {
  const volumeId = q.volume || (q.name.includes(':') ? q.name.slice(0, q.name.indexOf(':')) : '');
  const name = q.name.includes(':') ? q.name.slice(q.name.indexOf(':') + 1) : q.name;
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
    fileName: name.split('/').pop() || name,
    volumeId,
    targetParentId: null,
    direction: kind === 'download' ? 'download' : 'upload',
    size: 0,
    transferredBytes: 0,
    state,
    speedBps: 0,
    isChunkedResume: false,
    chunkSize: 0,
    currentChunk: 0,
    totalChunks: 0,
    createdAt: new Date().toISOString(),
    category: inferFileCategory(name, false),
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

  // 全局键盘：方向键移动光标，Enter 确认/切换开关，Esc 逐层退出。
  //
  // 输入：document keydown（冒泡阶段，让页面内控件先处理）
  // 输出：无；改焦点或滚动
  // 预期：输入框、下拉、可编辑区与已打开弹窗里一律不抢键；
  //       没有候选时不以 preventDefault 拦截，保留原生滚动
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.defaultPrevented) return;
      if (e.ctrlKey || e.altKey || e.metaKey) return;
      const target = e.target as HTMLElement | null;
      const tag = target?.tagName ?? '';
      if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || target?.isContentEditable) {
        return;
      }
      if (document.querySelector('dialog[open]')) return;

      const dir = matchesShortcut(e, shortcuts.moveUp)
        ? 'up'
        : matchesShortcut(e, shortcuts.moveDown)
          ? 'down'
          : matchesShortcut(e, shortcuts.moveLeft)
            ? 'left'
            : matchesShortcut(e, shortcuts.moveRight)
              ? 'right'
              : null;
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
  const [uploadTasks, setUploadTasks] = useState<TransferTask[]>([]);
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

  // 合并上传中、下载中与服务端排队任务；同名的活动上传优先，避免重复行
  const transfers = useMemo(() => {
    const active = new Set(
      uploadTasks
        .filter((t) => t.state === 'transferring' || t.state === 'queued')
        .map((t) => `${t.volumeId}:${t.fileName}`)
    );
    const queued = serverQueue.filter((q) => !active.has(`${q.volumeId}:${q.fileName}`));
    return [...uploadTasks, ...serverTransfers, ...queued];
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
      setActiveVolumeId((cur) => (cur && names.includes(cur) ? cur : names[0] ?? ''));
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
    setFilesLoading(true);
    const lists = await Promise.all(
      targets.map(async (name) => {
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
          return [] as ApiFile[];
        }
      })
    );
    setFiles(lists.flat().map(toFileItem));
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
    const onConfig = () => {
      void reloadSettings();
      scheduleState();
      scheduleFiles();
    };

    // 桌面壳与网页版共用 subscribePush：组件里不再出现传输通道判断
    const unsubscribe = subscribePush(
      { status: onStatus, config: onConfig },
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

  const handleUploadFiles = (fileList: FileList, volumeId: string, parentId: string | null) => {
    const incoming = Array.from(fileList);
    if (incoming.length === 0) return;
    // 上传前预检：单个文件不合格只跳过它，其余照常上传，
    // 不因为一个超大文件把整批都丢掉（跳过哪些要明说）
    const accepted = incoming.filter((file) => {
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
    // 文件夹上传由 input[webkitdirectory] 提供相对路径；普通选择时为空
    const rels = accepted.map(
      (file) => (file as File & { webkitRelativePath?: string }).webkitRelativePath ?? ''
    );
    // 同一次选择上传的文件共享批次号，概览页据此显示整批总进度
    const batchId = `batch-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`;
    const plan = accepted.map((file, index) => {
      const id = `task-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`;
      const task: TransferTask = {
        id,
        fileName: file.name,
        volumeId,
        targetParentId: parentId,
        direction: 'upload',
        size: file.size,
        transferredBytes: 0,
        state: 'queued',
        speedBps: 0,
        isChunkedResume: false,
        chunkSize: 0,
        currentChunk: 0,
        totalChunks: 1,
        createdAt: new Date().toISOString(),
        category: inferFileCategory(file.name, false),
        opKind: 'upload',
        batchId,
      };
      uploadInputs.current.set(id, {
        file,
        volumeId,
        parentId,
        rel: rels[index] ?? '',
        task,
      });
      return task;
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
    if (!item) return;
    const targetDir = targetParentId || '/';
    const dest = targetDir === '/' ? `/${newName}` : `${targetDir}/${newName}`;
    moveItems(item.volumeId, [itemId], targetDir, dest)
      .then(() => reloadFiles([item.volumeId]))
      .then(() => pushToast(t.files.itemMovedRenamedToast, 'success'))
      .catch((err) => pushToast(err instanceof Error ? err.message : String(err), 'error'));
  };

  const handleBatchMoveItems = (itemIds: string[], targetParentId: string | null) => {
    const vol = files.find((f) => f.id === itemIds[0])?.volumeId;
    if (!vol || itemIds.length === 0) return;
    moveItems(vol, itemIds, targetParentId || '/')
      .then(() => reloadFiles([vol]))
      .then(() => pushToast(t.files.itemMovedRenamedToast, 'success'))
      .catch((err) => pushToast(err instanceof Error ? err.message : String(err), 'error'));
  };

  const handleDeleteItems = (itemIds: string[]) => {
    const vol = files.find((f) => f.id === itemIds[0])?.volumeId;
    if (!vol || itemIds.length === 0) return;
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

  // 主题按钮：点一下切到下一档，浅色 → 深色 → 跟随系统 → 浅色
  //
  // 输入：theme 当前档位
  // 输出：下一档的图标与文案（按钮显示的是「点了会变成什么」）
  // 预期：三处主题按钮共用同一套映射，不会各写一遍
  const themeNext: ThemeMode = theme === 'light' ? 'dark' : theme === 'dark' ? 'system' : 'light';
  const ThemeNextIcon = themeNext === 'light' ? Sun : themeNext === 'dark' ? Moon : Monitor;
  const themeNextLabel =
    themeNext === 'light'
      ? t.header.themeLight
      : themeNext === 'dark'
        ? t.header.themeDark
        : t.header.themeSystem;
  const themeIconClass = `shrink-0 ${themeNext === 'light' ? 'text-amber-400' : 'text-zinc-700 dark:text-zinc-300'}`;

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

      {/* Phone Top Status Bar (Only visible on <768px phones; Tablet & Desktop have NO top bar) */}
      <header className="md:hidden sticky top-0 z-30 border-b border-zinc-200/80 dark:border-zinc-800/80 bg-white/85 dark:bg-[#0C0E14]/85 backdrop-blur-xl px-3.5 py-2">
        <div className="flex items-center justify-between gap-2">
          <motion.a
            href="#overview"
            whileTap={{ scale: 0.92 }}
            transition={IOS_BOUNCE_SPRING}
            onClick={(e) => {
              e.preventDefault();
              setActiveTab('overview');
            }}
            className="flex items-center gap-2 min-w-0 shrink-0"
          >
            <BrandEmblemSvg className="w-7 h-7 shrink-0" />
            <div className="min-w-0">
              <div dir="ltr" className="text-sm font-bold tracking-tight text-zinc-950 dark:text-white leading-tight">
                {t.appName}
              </div>
              <div className="text-[10px] font-mono text-emerald-600 dark:text-emerald-400 truncate leading-tight">
                {formatBytes(summary.totalBytes, 1, locale)}
              </div>
            </div>
          </motion.a>

          <div className="flex items-center gap-1.5 min-w-0">
            <div className="relative flex items-center">
              <Globe className="w-3.5 h-3.5 text-zinc-500 dark:text-zinc-400 absolute start-2.5 pointer-events-none" />
              <Select
                aria-label={t.sidebar.languageLabel}
                value={locale}
                onChange={(e) => setLocale(e.target.value as Locale)}
                className="h-8 ps-7 pe-2 text-xs max-w-[116px] truncate"
              >
                {LOCALE_OPTIONS.map((opt) => (
                  <option
                    key={opt.id}
                    value={opt.id}
                    className="bg-white dark:bg-zinc-900 text-zinc-900 dark:text-zinc-100"
                  >
                    {opt.label}
                  </option>
                ))}
              </Select>
            </div>

            <Button
              type="button"
              variant="outline"
              size="icon"
              onClick={() => setTheme(themeNext)}
              aria-label={themeNextLabel}
            >
              <ThemeNextIcon className={`w-3.5 h-3.5 ${themeIconClass}`} />
            </Button>
          </div>
        </div>
      </header>

      {/* Main Workspace Canvas: Collapsible iOS/iPadOS/macOS Spring Sidebar (md+) + Main Content Viewport */}
      <div className="flex-1 flex min-w-0">
        <motion.aside
          initial={false}
          animate={{ width: sidebarCollapsed ? 76 : 256 }}
          transition={IOS_SPRING}
          className="hidden md:flex shrink-0 flex-col border-e border-zinc-200/80 dark:border-zinc-800/80 bg-white/80 dark:bg-[#0C0E14]/90 backdrop-blur-xl sticky top-0 h-screen overflow-hidden"
        >
          <CustomScrollArea className="flex-1 h-full" viewportClassName="p-3.5">
            <div className="space-y-5">
            {/* Brand Identity + Collapse Toggle */}
            <div className="flex items-center justify-between gap-2 px-1">
              {!sidebarCollapsed && (
                <motion.a
                  href="#overview"
                  whileTap={{ scale: 0.93 }}
                  whileHover={{ scale: 1.015 }}
                  transition={IOS_BOUNCE_SPRING}
                  onClick={(e) => {
                    e.preventDefault();
                    setActiveTab('overview');
                  }}
                  className="flex items-center gap-2.5 min-w-0"
                >
                  <BrandEmblemSvg className="w-8 h-8 shrink-0" />
                  <div className="min-w-0">
                    <div
                      dir="ltr"
                      className="text-sm font-bold text-zinc-950 dark:text-white tracking-tight truncate"
                    >
                      {t.appName}
                    </div>

                  </div>
                </motion.a>
              )}

              <Button
                type="button"
                variant="ghost"
                size="icon"
                onClick={() => setSidebarCollapsed((c) => !c)}
                aria-label={sidebarCollapsed ? t.sidebar.expandSidebar : t.sidebar.collapseSidebar}
                title={sidebarCollapsed ? t.sidebar.expandSidebar : t.sidebar.collapseSidebar}
                className={sidebarCollapsed ? 'mx-auto' : 'ms-auto'}
              >
                {sidebarCollapsed ? (
                  <PanelLeftOpen className="w-4 h-4" />
                ) : (
                  <PanelLeftClose className="w-4 h-4" />
                )}
              </Button>
            </div>

            {/* Navigation Section with iOS Liquid Spring Pill & Tactile Press */}
            <LayoutGroup id="desktop-sidebar-nav">
              <nav aria-label={t.a11y.primaryNav} className="space-y-1">
                {navItems.map((item) => {
                  const isActive = activeTab === item.id;
                  return (
                    <motion.a
                      key={item.id}
                      href={`#${item.id}`}
                      whileTap={{ scale: 0.92 }}
                      whileHover={{ scale: 1.015 }}
                      transition={IOS_BOUNCE_SPRING}
                      onClick={(e) => {
                        e.preventDefault();
                        runViewTransition(() => setActiveTab(item.id));
                      }}
                      title={sidebarCollapsed ? item.label : undefined}
                      aria-current={isActive ? 'page' : undefined}
                      className={`relative w-full h-10 ${
                        sidebarCollapsed ? 'justify-center px-0' : 'justify-between px-3'
                      } rounded-xl text-xs font-medium flex items-center transition-colors select-none z-10 ${
                        isActive
                          ? 'text-white font-semibold'
                          : 'text-zinc-600 dark:text-zinc-400 hover:text-zinc-950 dark:hover:text-white hover:bg-zinc-200/50 dark:hover:bg-white/[0.04]'
                      }`}
                    >
                      {isActive && (
                        <motion.span
                          layoutId="sidebar-active-pill"
                          transition={IOS_SPRING}
                          className="absolute inset-0 rounded-xl bg-[#007AFF] dark:bg-[#0A84FF] shadow-xs -z-10"
                        />
                      )}
                      <span className="flex items-center gap-2.5 min-w-0">
                        {item.icon}
                        {!sidebarCollapsed && <span className="truncate">{item.label}</span>}
                      </span>
                      {!sidebarCollapsed && item.id === 'files' && (
                        <span
                          className={`font-mono text-[11px] tabular-nums shrink-0 ${
                            isActive ? 'text-white/90' : 'text-zinc-400 dark:text-zinc-500'
                          }`}
                        >
                          {formatNumber(summary.totalFiles, locale)}
                        </span>
                      )}
                      {!sidebarCollapsed && item.id === 'downloads' && (
                        <span
                          className={`font-mono text-[11px] tabular-nums shrink-0 ${
                            isActive ? 'text-white/90' : 'text-zinc-400 dark:text-zinc-500'
                          }`}
                        >
                          {formatNumber(downloads.length, locale)}
                        </span>
                      )}
                    </motion.a>
                  );
                })}
              </nav>
            </LayoutGroup>

            {/* Total Used Header + Per-Volume Usage (xxG xx%) */}
            {!sidebarCollapsed && (
              <div className="space-y-2 pt-4 border-t border-zinc-200/80 dark:border-zinc-800/80">
                <div className="px-2.5 py-1 flex items-center justify-between gap-2 text-xs font-medium text-zinc-500 dark:text-zinc-400">
                  <span className="truncate">{t.sidebar.totalUsed}</span>
                  <span
                    dir="ltr"
                    className="font-mono font-semibold tabular-nums text-zinc-900 dark:text-zinc-100 shrink-0"
                  >
                    {formatBytes(summary.totalBytes, 1, locale)}
                  </span>
                </div>
                {summary.perVolume.map(({ volume, totalBytes, sharePct = 0 }) => {
                  const isCurrentVol = activeVolumeId === volume.id && activeTab === 'files';
                  const visualWidth = Math.min(100, Math.max(6, sharePct));
                  return (
                    <motion.button
                      key={volume.id}
                      type="button"
                      whileTap={{ scale: 0.93 }}
                      whileHover={{ scale: 1.015 }}
                      transition={IOS_BOUNCE_SPRING}
                      onClick={() => {
                        setActiveVolumeId(volume.id);
                        setCurrentFolderId(null);
                        setActiveTab('files');
                      }}
                      className={`w-full px-2.5 py-2.5 rounded-xl text-start transition-colors space-y-1.5 cursor-pointer ${
                        isCurrentVol
                          ? 'bg-[#007AFF]/10 text-[#007AFF] dark:text-[#0A84FF] ring-1 ring-[#007AFF]/30'
                          : 'text-zinc-700 dark:text-zinc-300 hover:bg-zinc-200/50 dark:hover:bg-white/[0.04]'
                      }`}
                    >
                      <div className="flex items-center justify-between gap-2">
                        <span className="flex items-center gap-2 min-w-0">
                          <HardDrive className="w-3.5 h-3.5 shrink-0 opacity-75" />
                          <span dir="ltr" className="text-xs font-mono font-semibold truncate">
                            {volumeDisplayName(volume.name, volume.title)}
                          </span>
                        </span>
                        <span
                          dir="ltr"
                          className="text-[11px] font-mono tabular-nums text-zinc-500 dark:text-zinc-400 shrink-0"
                        >
                          {formatBytes(totalBytes, 1, locale)} {formatPercent(sharePct, locale)}
                        </span>
                      </div>
                      <div className="w-full h-1.5 rounded-full bg-zinc-200/80 dark:bg-zinc-800 overflow-hidden">
                        <motion.div
                          initial={false}
                          animate={{ width: `${visualWidth}%` }}
                          transition={IOS_SOFT_SPRING}
                          className="h-full bg-[#007AFF] dark:bg-[#0A84FF] rounded-full"
                        />
                      </div>
                    </motion.button>
                  );
                })}
              </div>
            )}
          </div>

          {/* Bottom Sidebar Loopback Telemetry + Multi-Language Selector & Theme Controls */}
          <div className="space-y-3 pt-4 border-t border-zinc-200/80 dark:border-zinc-800/80">
            {!sidebarCollapsed && (
              <div className="p-3 rounded-xl bg-zinc-100/80 dark:bg-zinc-900/70 border border-zinc-200/60 dark:border-zinc-800/80">
                <div className="flex items-center gap-2 text-xs">
                  {connectionStatus === 'connected' ? (
                    <CheckCircle2 className="w-4 h-4 text-emerald-500 shrink-0" />
                  ) : connectionStatus === 'connecting' || connectionStatus === 'reconnecting' ? (
                    <Loader2 className="w-4 h-4 text-amber-500 animate-spin shrink-0" />
                  ) : (
                    <AlertTriangle className="w-4 h-4 text-red-500 shrink-0" />
                  )}
                  <span className="text-zinc-500 dark:text-zinc-400 truncate">
                    {t.connection.label}:
                  </span>
                  <span
                    className={`font-medium truncate ${
                      connectionStatus === 'connected'
                        ? 'text-emerald-600 dark:text-emerald-400'
                        : connectionStatus === 'connecting' || connectionStatus === 'reconnecting'
                        ? 'text-amber-600 dark:text-amber-400'
                        : 'text-red-600 dark:text-red-400'
                    }`}
                  >
                    {t.connection[connectionStatus]}
                  </span>
                </div>
              </div>
            )}

            {sidebarCollapsed ? (
              <div className="flex flex-col gap-2">
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  onClick={cycleNextLocale}
                  aria-label={t.sidebar.languageLabel}
                  title={`${t.sidebar.languageLabel}: ${localeMeta.label}`}
                  className="px-0 justify-center"
                >
                  <Globe className="w-3.5 h-3.5 shrink-0" />
                </Button>

                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  onClick={() => setTheme(themeNext)}
                  aria-label={themeNextLabel}
                  className="px-0 justify-center"
                >
                  <ThemeNextIcon className={`w-3.5 h-3.5 ${themeIconClass}`} />
                </Button>
              </div>
            ) : (
              <div className="flex items-center gap-2">
                <div className="relative flex items-center flex-1 min-w-0">
                  <Globe className="w-3.5 h-3.5 text-zinc-500 dark:text-zinc-400 absolute start-2.5 pointer-events-none" />
                  <Select
                    aria-label={t.sidebar.languageLabel}
                    value={locale}
                    onChange={(e) => setLocale(e.target.value as Locale)}
                    className="h-8 w-full ps-7 pe-2 text-xs rounded-lg truncate"
                  >
                    {LOCALE_OPTIONS.map((opt) => (
                      <option
                        key={opt.id}
                        value={opt.id}
                        className="bg-white dark:bg-zinc-900 text-zinc-900 dark:text-zinc-100"
                      >
                        {opt.label}
                      </option>
                    ))}
                  </Select>
                </div>

                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  onClick={() => setTheme(themeNext)}
                  aria-label={themeNextLabel}
                  title={themeNextLabel}
                  className="px-2.5 shrink-0"
                >
                  <ThemeNextIcon className={`w-3.5 h-3.5 ${themeIconClass}`} />
                  <span className="truncate max-w-[72px]">{themeNextLabel}</span>
                </Button>
              </div>
            )}
          </div>
          </CustomScrollArea>
        </motion.aside>

        {/* Custom iOS / macOS Interactive Floating Window Scrollbar */}
        <WindowCustomScrollbar />

        {/* Main Content Viewport: Persistent, velocity-preserving iOS spring (zero unmount flash, 100% mid-flight interruptible) */}
        <main
          id="main"
          tabIndex={-1}
          className="flex-1 min-w-0 px-3.5 sm:px-6 lg:px-8 pt-4 sm:pt-7 pb-24 md:pb-8 max-w-5xl mx-auto w-full overflow-x-hidden outline-none"
        >
          {/* 常驻错误横幅：后台加载失败与实时连接断开必须一直可见，直到恢复或手动关闭 */}
          <AnimatePresence initial={false}>
            {problemList.length > 0 && (
              <motion.div
                role="alert"
                initial={{ opacity: 0, y: -8 }}
                animate={{ opacity: 1, y: 0 }}
                exit={{ opacity: 0, y: -8 }}
                transition={IOS_SOFT_SPRING}
                className="mb-4 rounded-2xl border border-red-300/70 dark:border-red-900/70 bg-red-50/95 dark:bg-red-950/60 px-4 py-3"
              >
                <div className="flex items-start gap-2.5">
                  <AlertCircle className="w-4 h-4 mt-0.5 shrink-0 text-red-600 dark:text-red-400" />
                  <div className="min-w-0 flex-1 space-y-1">
                    <p className="text-xs font-semibold text-red-700 dark:text-red-300">
                      {t.errors.title}
                    </p>
                    {problemList.map((msg) => (
                      <p
                        key={msg}
                        className="text-[11px] leading-relaxed text-red-700/90 dark:text-red-200/90 break-words"
                      >
                        {msg}
                      </p>
                    ))}
                  </div>
                  <div className="flex items-center gap-1 shrink-0">
                    <Button type="button" variant="outline" size="sm" onClick={retryLoad}>
                      {t.errors.retry}
                    </Button>
                    <Button
                      type="button"
                      variant="ghost"
                      size="icon"
                      onClick={() => setProblems({})}
                      aria-label={t.errors.dismiss}
                    >
                      <X className="w-4 h-4" />
                    </Button>
                  </div>
                </div>
              </motion.div>
            )}
          </AnimatePresence>
          <motion.div
            style={{
              y: viewSpring.y,
              scale: viewSpring.scale,
              opacity: viewSpring.opacity,
              transformOrigin: 'top center',
            }}
          >
            {activeTab === 'overview' && (
              <OverviewView
                t={t}
                locale={locale}
                volumes={volumes}
                files={files}
                transfers={transfers}
                activeVolumeId={activeVolumeId}
                onSelectVolumeAndBrowse={(volId) => {
                  setActiveVolumeId(volId);
                  setCurrentFolderId(null);
                  setActiveTab('files');
                }}
                onUploadFiles={handleUploadFiles}
                onToggleTransferPause={handleToggleTransferPause}
                onRetryTransfer={handleRetryTransfer}
                onCancelTransfer={handleCancelTransfer}
                onClearCompletedTransfers={handleClearCompletedTransfers}
                onSetCover={caps.cover ? handleSetCover : undefined}
              />
            )}

            {activeTab === 'files' && (
              <FilesView
                t={t}
                locale={locale}
                volumes={volumes}
                activeVolumeId={activeVolumeId}
                onChangeVolume={(volId) => {
                  setActiveVolumeId(volId);
                  setCurrentFolderId(null);
                }}
                currentFolderId={currentFolderId}
                onNavigateFolder={setCurrentFolderId}
                files={files}
                loading={filesLoading}
                connectionStatus={connectionStatus}
                onUploadFiles={handleUploadFiles}
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
          </motion.div>
        </main>
      </div>

      {/* Phone Native iOS Frosted Bottom Tab Bar (<768px) */}
      <LayoutGroup id="phone-bottom-dock">
        <nav
          aria-label={t.a11y.primaryNav}
          className="md:hidden fixed bottom-0 inset-x-0 z-40 border-t border-zinc-200/80 dark:border-zinc-800/80 bg-white/88 dark:bg-[#0C0E14]/92 backdrop-blur-2xl px-2 py-1.5 pb-[max(0.375rem,env(safe-area-inset-bottom))]"
        >
          <div className="flex gap-1 max-w-md mx-auto">
            {navItems.map((item) => {
              const isActive = activeTab === item.id;
              const badgeCount =
                item.id === 'files'
                  ? summary.totalFiles
                  : item.id === 'downloads'
                  ? downloads.length
                  : null;
              return (
                <motion.a
                  key={item.id}
                  href={`#${item.id}`}
                  whileTap={{ scale: 0.86 }}
                  transition={IOS_BOUNCE_SPRING}
                  onClick={(e) => {
                    e.preventDefault();
                    runViewTransition(() => setActiveTab(item.id));
                  }}
                  aria-current={isActive ? 'page' : undefined}
                  className={`relative flex flex-1 min-w-0 flex-col items-center justify-center py-1.5 px-1 rounded-xl text-[11px] font-medium select-none transition-colors z-10 ${
                    isActive
                      ? 'text-[#007AFF] dark:text-[#0A84FF] font-semibold'
                      : 'text-zinc-500 dark:text-zinc-400 hover:text-zinc-900 dark:hover:text-zinc-200'
                  }`}
                >
                  {isActive && (
                    <motion.span
                      layoutId="phone-bottom-tab-pill"
                      transition={IOS_SPRING}
                      className="absolute inset-0 rounded-xl bg-[#007AFF]/12 dark:bg-[#0A84FF]/18 -z-10"
                    />
                  )}
                  <div className="relative flex items-center justify-center">
                    {item.icon}
                    {badgeCount !== null && badgeCount > 0 && (
                      <span className="absolute -top-1.5 -end-3 min-w-[15px] h-[15px] px-1 rounded-full bg-[#007AFF] text-white text-[9px] font-mono font-bold inline-flex items-center justify-center leading-none">
                        {formatNumber(badgeCount, locale)}
                      </span>
                    )}
                  </div>
                  <span className="mt-1 truncate max-w-full leading-none">{item.label}</span>
                </motion.a>
              );
            })}
          </div>
        </nav>
      </LayoutGroup>

      {/* File Details & Media Preview Modal */}
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

      {/* iOS Dynamic Toast Notification Stack */}
      <div className="fixed bottom-20 md:bottom-4 end-4 z-50 flex flex-col gap-2 max-w-md w-[calc(100vw-2rem)] sm:w-auto pointer-events-none">
        <AnimatePresence>
          {toasts.map((toast) => (
            <motion.div
              key={toast.id}
              layout
              initial={{ opacity: 0, y: 16, scale: 0.94 }}
              animate={{ opacity: 1, y: 0, scale: 1 }}
              exit={{ opacity: 0, scale: 0.94, transition: { duration: 0.15 } }}
              transition={IOS_SPRING}
              // 错误用 alert 立即播报，其余用 status 排队播报；
              // 放在每条上而不是外层，避免同一个节点挂两个 live 区重复朗读
              role={toast.type === 'error' ? 'alert' : 'status'}
              className={`pointer-events-auto flex items-center justify-between gap-3 px-4 py-2.5 rounded-2xl shadow-lg border text-xs font-medium backdrop-blur-xl ${
                toast.type === 'error'
                  ? 'bg-red-950/90 text-red-100 border-red-800/80'
                  : 'bg-zinc-900/90 text-zinc-100 border-white/10'
              }`}
            >
              <div className="flex items-center gap-2.5">
                {toast.type === 'error' ? (
                  <AlertCircle className="w-4 h-4 text-red-400 shrink-0" />
                ) : toast.type === 'pending' ? (
                  <Loader2 className="w-4 h-4 text-sky-400 shrink-0 animate-spin" />
                ) : (
                  <CheckCircle2 className="w-4 h-4 text-emerald-400 shrink-0" />
                )}
                <span className="leading-relaxed">{toast.text}</span>
                {toast.action && (
                  <button
                    type="button"
                    onClick={() => {
                      toast.action?.onClick();
                      setToasts((prev) => prev.filter((item) => item.id !== toast.id));
                    }}
                    className="ms-1 px-2.5 py-1 rounded-lg bg-white/10 hover:bg-white/20 text-[11px] font-semibold shrink-0 cursor-pointer"
                  >
                    {toast.action.label}
                  </button>
                )}
              </div>
              <button
                type="button"
                onClick={() =>
                  setToasts((prev) => prev.filter((item) => item.id !== toast.id))
                }
                aria-label={t.a11y.dismissToast}
                className="w-7 h-7 rounded-lg inline-flex items-center justify-center text-zinc-400 hover:text-white shrink-0 cursor-pointer"
              >
                <X className="w-3.5 h-3.5" />
              </button>
            </motion.div>
          ))}
        </AnimatePresence>
      </div>
    </div>
  );
}
