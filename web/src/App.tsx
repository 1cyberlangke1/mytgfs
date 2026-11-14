import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  AlertCircle,
  CheckCircle2,
  FolderTree,
  Globe,
  HardDrive,
  HardDriveDownload,
  LayoutDashboard,
  Moon,
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
  FileCategory,
  FileItem,
  IndexStatus,
  Locale,
  NavTab,
  ThemeMode,
  TransferTask,
  VolumeInfo,
} from './types';
import { computeOverviewSummary, formatBytes, inferFileCategory } from './utils/fsLogic';
import {
  DICTIONARY,
  LOCALE_OPTIONS,
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
  attachVolume,
  createVolume,
  deleteVolume,
  downloadToDir,
  getDownloads,
  getSettings,
  getState,
  listFiles,
  mkdir,
  moveItems,
  openDownloads,
  removeDownload,
  removeItems,
  saveSettings,
  triggerSync,
  uploadFile,
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

const STORAGE_KEYS = {
  tab: 'mytgfs_active_tab_v1',
  locale: 'mytgfs_locale_v1',
  theme: 'mytgfs_theme_v1',
  activeVol: 'mytgfs_active_vol_v1',
};

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

interface ToastMessage {
  id: string;
  type: 'success' | 'error';
  text: string;
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
// 输出：state=queued 的任务，opKind 保留原始操作类型
// 预期：与正在上传的同名任务去重，避免面板出现两行
function queueFromServer(q: ApiQueueItem, index: number): TransferTask {
  const volumeId = q.volume || (q.name.includes(':') ? q.name.slice(0, q.name.indexOf(':')) : '');
  const name = q.name.includes(':') ? q.name.slice(q.name.indexOf(':') + 1) : q.name;
  const kind = (q.kind || 'upload') as TransferTask['opKind'];
  return {
    id: `q-${index}-${q.id}`,
    fileName: name.split('/').pop() || name,
    volumeId,
    targetParentId: null,
    direction: kind === 'download' ? 'download' : 'upload',
    size: 0,
    transferredBytes: 0,
    state: q.state === 'running' ? 'transferring' : q.state === 'failed' ? 'failed' : 'queued',
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

  const [activeTab, setActiveTab] = useState<NavTab>(() =>
    loadFromStorage<NavTab>(STORAGE_KEYS.tab, 'overview')
  );
  const [locale, setLocale] = useState<Locale>(() => {
    const saved = loadFromStorage<Locale | null>(STORAGE_KEYS.locale, null);
    if (saved && DICTIONARY[saved]) return saved;
    return 'zh';
  });
  const [theme, setTheme] = useState<ThemeMode>(() =>
    loadFromStorage<ThemeMode>(STORAGE_KEYS.theme, 'dark')
  );
  const [connectionStatus, setConnectionStatus] = useState<ConnectionStatus>('connecting');

  const [volumes, setVolumes] = useState<VolumeInfo[]>([]);
  const [files, setFiles] = useState<FileItem[]>([]);
  const [uploadTasks, setUploadTasks] = useState<TransferTask[]>([]);
  const [serverTransfers, setServerTransfers] = useState<TransferTask[]>([]);
  const [serverQueue, setServerQueue] = useState<TransferTask[]>([]);
  const [settings, setSettings] = useState<AppSettings>(EMPTY_SETTINGS);
  // 下载目录列表由服务端扫描磁盘得到，页面只负责展示与刷新
  const [downloads, setDownloads] = useState<DownloadItem[]>([]);
  const [downloadsDir, setDownloadsDir] = useState('');

  const [activeVolumeId, setActiveVolumeId] = useState<string>(() =>
    loadFromStorage<string>(STORAGE_KEYS.activeVol, '')
  );
  const [currentFolderId, setCurrentFolderId] = useState<string | null>(null);

  const [previewFile, setPreviewFile] = useState<FileItem | null>(null);
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
  }>({
    createVolume: false,
    deleteVolume: false,
    attachVolume: false,
    listChannels: false,
    login: false,
    autostart: false,
  });
  // 封面图地址带版本号，改完封面立刻刷新，不依赖浏览器缓存过期
  const [coverVersion, setCoverVersion] = useState(0);

  const t = DICTIONARY[locale] || DICTIONARY.zh;
  const localeMeta = getLocaleMeta(locale);
  const summary = computeOverviewSummary(volumes, files);
  const problemList = useMemo(() => Object.values(problems), [problems]);
  const viewSpring = useInterruptibleViewSpring(activeTab);

  // uploadAbort 记录每个上传任务的取消句柄；uploadInputs 保留 File 以便暂停后重发
  const uploadAbort = useRef(new Map<string, AbortController>());
  const uploadInputs = useRef(new Map<string, { file: File; volumeId: string; parentId: string | null }>());
  // 速度采样：只展示真实测得的字节增量，不编造速率
  const uploadSpeed = useRef(new Map<string, { bytes: number; at: number }>());
  const downloadSpeed = useRef(new Map<string, { bytes: number; at: number }>());
  // 文案与语言用 ref 读取：后台回调不因切语言而重建连接或定时器
  const tRef = useRef(t);
  tRef.current = t;
  const localeRef = useRef(locale);
  localeRef.current = locale;

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
    const next = LOCALE_OPTIONS[(idx + 1) % LOCALE_OPTIONS.length];
    setLocale(next.id);
  };

  const pushToast = (text: string, type: 'success' | 'error' = 'success') => {
    const id = `toast-${Date.now()}-${Math.random().toString(36).slice(2, 6)}`;
    setToasts((prev) => [...prev.slice(-3), { id, type, text }]);
    window.setTimeout(() => {
      setToasts((prev) => prev.filter((item) => item.id !== id));
    }, 4500);
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
          hasCover: Boolean(v.cover),
        }))
      );
      setActiveVolumeId((cur) => (cur && names.includes(cur) ? cur : names[0] ?? ''));
      setConnectionStatus(connOf(st.status));
      setServerTransfers(sampleDownloads(st.status?.transfers ?? []));
      setServerQueue((st.status?.queue ?? []).map(queueFromServer));
      setCaps({
        createVolume: Boolean(st.caps?.createVolume),
        deleteVolume: Boolean(st.caps?.deleteVolume),
        attachVolume: Boolean(st.caps?.attachVolume),
        listChannels: Boolean(st.caps?.listChannels),
        login: Boolean(st.caps?.login),
        autostart: Boolean(st.caps?.autostart),
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
    if (targets.length === 0) return;
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

  // 首屏：只拉一次状态 + 设置；之后完全由 SSE 事件驱动，不做定时轮询。
  // 卷可能还在挂载：服务端打开卷后会推 status，前端收到事件后自然刷新。
  useEffect(() => {
    void (async () => {
      const names = await reloadState();
      if (names && names.length > 0) await reloadFiles(names);
      await reloadSettings();
    })();
  }, [reloadState, reloadFiles, reloadSettings]);

  // 事件驱动：服务端有变化时才推送，前端收到后拉最新快照（取代定时轮询）
  useEffect(() => {
    // 状态与文件分别防抖：进度类事件密集，避免每次事件都全量拉取
    let stateTimer = 0;
    let filesTimer = 0;
    const scheduleState = () => {
      window.clearTimeout(stateTimer);
      stateTimer = window.setTimeout(() => void reloadState(), 200);
    };
    const scheduleFiles = () => {
      window.clearTimeout(filesTimer);
      filesTimer = window.setTimeout(() => void reloadFiles(), 1500);
    };

    const es = new EventSource('/api/events');
    // 断线时浏览器会一直静默重连；不提示的话用户会以为数据是实时的
    es.onerror = () => setProblem('events', tRef.current.errors.connection);
    const onStatus = () => {
      clearProblem('events');
      scheduleState();
      scheduleFiles();
    };
    const onConfig = () => {
      void reloadSettings();
      scheduleState();
      scheduleFiles();
    };
    es.addEventListener('status', onStatus);
    es.addEventListener('config', onConfig);
    // 断线重连后补拉一次，覆盖断线期间丢失的事件
    es.addEventListener('ready', onStatus);

    return () => {
      window.clearTimeout(stateTimer);
      window.clearTimeout(filesTimer);
      es.close();
    };
  }, [reloadState, reloadFiles, reloadSettings]);

  // 主题
  useEffect(() => {
    const root = document.documentElement;
    if (theme === 'dark') root.classList.add('dark');
    else root.classList.remove('dark');
    window.localStorage.setItem(STORAGE_KEYS.theme, JSON.stringify(theme));
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

  // 单文件上传：更新任务进度，成功后刷新索引
  const runUpload = async (task: TransferTask, file: File, volumeId: string, parentId: string | null) => {
    const ctl = new AbortController();
    uploadAbort.current.set(task.id, ctl);
    setUploadTasks((prev) =>
      prev.map((tr) => (tr.id === task.id ? { ...tr, state: 'transferring' } : tr))
    );
    try {
      await uploadFile(
        volumeId,
        parentId || '/',
        '',
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
        ctl.signal
      );
      setUploadTasks((prev) =>
        prev.map((tr) =>
          tr.id === task.id
            ? { ...tr, state: 'completed', transferredBytes: tr.size, speedBps: 0 }
            : tr
        )
      );
      uploadAbort.current.delete(task.id);
      await reloadFiles();
      await reloadState();
    } catch (err) {
      const aborted = err instanceof Error && err.message === 'aborted';
      setUploadTasks((prev) =>
        prev.map((tr) =>
          tr.id === task.id
            ? {
                ...tr,
                state: aborted ? 'paused' : 'failed',
                speedBps: 0,
                errorMessage: aborted ? undefined : err instanceof Error ? err.message : String(err),
              }
            : tr
        )
      );
      uploadAbort.current.delete(task.id);
      if (!aborted) pushToast(err instanceof Error ? err.message : String(err), 'error');
    }
  };

  const handleUploadFiles = (fileList: FileList, volumeId: string, parentId: string | null) => {
    const incoming = Array.from(fileList);
    if (incoming.length === 0) return;
    const plan = incoming.map((file) => {
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
      };
      uploadInputs.current.set(id, { file, volumeId, parentId });
      return task;
    });
    setUploadTasks((prev) => [...prev, ...plan]);
    pushToast(
      interpolate(t.files.uploadStartedToast, { name: plan[0].fileName }, locale),
      'success'
    );
    for (let i = 0; i < plan.length; i++) {
      void runUpload(plan[i], incoming[i], volumeId, parentId);
    }
  };

  const handleToggleTransferPause = (taskId: string) => {
    const task = uploadTasks.find((tr) => tr.id === taskId);
    if (!task) return;
    if (task.state === 'transferring' || task.state === 'queued') {
      uploadAbort.current.get(taskId)?.abort();
      return;
    }
    if (task.state === 'paused') {
      const input = uploadInputs.current.get(taskId);
      if (!input) return;
      void runUpload(task, input.file, input.volumeId, input.parentId);
    }
  };

  const handleCancelTransfer = (taskId: string) => {
    uploadAbort.current.get(taskId)?.abort();
    uploadAbort.current.delete(taskId);
    uploadInputs.current.delete(taskId);
    setUploadTasks((prev) => prev.filter((tr) => tr.id !== taskId));
  };

  const handleClearCompletedTransfers = () => {
    setUploadTasks((prev) => prev.filter((tr) => tr.state !== 'completed'));
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

  // handleDownloadFile 让服务端把文件写进下载目录，再由页面刷新列表
  const handleDownloadFile = (file: FileItem) => {
    downloadToDir(file.volumeId, file.id)
      .then((res) => {
        setDownloadsDir(res.dir);
        return reloadDownloads();
      })
      .then(() =>
        pushToast(interpolate(t.downloads.savedToast, { name: file.name }, locale), 'success')
      )
      .catch((err) => pushToast(err instanceof Error ? err.message : String(err), 'error'));
  };

  // handleRemoveDownload 只删本地副本，云端文件不受影响
  const handleRemoveDownload = (name: string) =>
    removeDownload(name)
      .then(() => reloadDownloads())
      .then(() => pushToast(t.downloads.removedToast, 'success'))
      .catch((err) => pushToast(err instanceof Error ? err.message : String(err), 'error'));

  // handleOpenDownloads 用系统文件管理器打开下载目录
  const handleOpenDownloads = () => {
    openDownloads()
      .then(() => undefined)
      .catch((err) => {
        const msg = err instanceof Error ? err.message : String(err);
        pushToast(`${t.downloads.openFailedToast}：${msg}`, 'error');
      });
  };

  // handleSaveSettings 只提交卡片里的键，成功后本地合并，不再整页重拉
  const handleSaveSettings = (values: Record<string, string>) => {
    return saveSettings(values).then(() => {
      setSettings((prev) => ({
        ...prev,
        values: { ...prev.values, ...values },
      }));
    });
  };


  // handleCreateVolume 在 Telegram 侧建频道并写入配置，成功后刷新卷列表
  const handleCreateVolume = (name: string, title: string) => {
    createVolume(name, title)
      .then(() => Promise.all([reloadState(), reloadFiles()]))
      .then(() => pushToast(t.volume.createSuccess.replace('{name}', name), 'success'))
      .catch((err) => pushToast(err instanceof Error ? err.message : String(err), 'error'));
  };

  // handleAttachVolume 挂载已有频道：名称 + chat_id，后端只读校验后写配置
  const handleAttachVolume = (name: string, chatId: number) => {
    attachVolume(name, chatId)
      .then(() => Promise.all([reloadState(), reloadFiles()]))
      .then(() => pushToast(t.volume.attachSuccess.replace('{name}', name), 'success'))
      .catch((err) => pushToast(err instanceof Error ? err.message : String(err), 'error'));
  };

  // handleDeleteVolume 删卷不可撤销：删频道 → 移出配置 → 删索引
  const handleDeleteVolume = (name: string) => {
    deleteVolume(name)
      .then(() => Promise.all([reloadState(), reloadFiles()]))
      .then(() => pushToast(t.volume.deleteSuccess.replace('{name}', name), 'success'))
      .catch((err) => pushToast(err instanceof Error ? err.message : String(err), 'error'));
  };

  // handleSetCover 把图片以保留名写进卷根目录，服务端据此生成封面
  const handleSetCover = (volume: string, file: File) => {
    const dot = file.name.lastIndexOf('.');
    const ext = dot >= 0 ? file.name.slice(dot + 1).toLowerCase() : 'jpg';
    uploadFile(volume, '/', `.cover.${ext}`, file)
      .then(() => {
        setCoverVersion((v) => v + 1);
        return Promise.all([reloadFiles([volume]), reloadState()]);
      })
      .then(() => pushToast(t.volume.coverSaved, 'success'))
      .catch((err) => pushToast(err instanceof Error ? err.message : String(err), 'error'));
  };

  // handleRemoveCover 删除卷根目录里的保留封面文件
  const handleRemoveCover = (volume: string) => {
    const covers = files.filter(
      (f) => f.volumeId === volume && !f.isDir && /^\.?cover\.(jpe?g|png|gif|webp)$/i.test(f.name)
    );
    if (covers.length === 0) {
      pushToast(t.volume.noCover, 'error');
      return;
    }
    removeItems(
      volume,
      covers.map((c) => c.id)
    )
      .then(() => {
        setCoverVersion((v) => v + 1);
        return Promise.all([reloadFiles([volume]), reloadState()]);
      })
      .then(() => pushToast(t.volume.coverRemoved, 'success'))
      .catch((err) => pushToast(err instanceof Error ? err.message : String(err), 'error'));
  };


  const [sidebarCollapsed, setSidebarCollapsed] = useState(false);

  const navItems: { id: NavTab; label: string; icon: React.ReactNode }[] = [
    { id: 'overview', label: t.nav.overview, icon: <LayoutDashboard className="w-4 h-4 shrink-0" /> },
    { id: 'files', label: t.nav.files, icon: <FolderTree className="w-4 h-4 shrink-0" /> },
    { id: 'settings', label: t.nav.settings, icon: <Sliders className="w-4 h-4 shrink-0" /> },
    { id: 'downloads', label: t.nav.downloads, icon: <HardDriveDownload className="w-4 h-4 shrink-0" /> },
  ];

  return (
    <div
      dir={localeMeta.dir}
      className="min-h-screen flex flex-col bg-[#F5F5F7] dark:bg-[#090A0F] text-zinc-900 dark:text-zinc-100"
    >
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
              onClick={() => setTheme((th) => (th === 'dark' ? 'light' : 'dark'))}
              aria-label={theme === 'dark' ? t.header.themeLight : t.header.themeDark}
            >
              {theme === 'dark' ? (
                <Sun className="w-3.5 h-3.5 text-amber-400" />
              ) : (
                <Moon className="w-3.5 h-3.5 text-zinc-700" />
              )}
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
                        setActiveTab(item.id);
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
                {summary.perVolume.map(({ volume, totalBytes, sharePct }) => {
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
                            {volume.name}
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
              <div className="p-3 rounded-xl bg-zinc-100/80 dark:bg-zinc-900/70 border border-zinc-200/60 dark:border-zinc-800/80 space-y-1 text-[11px]">
                <div className="flex items-center justify-between gap-2 text-zinc-500 dark:text-zinc-400">
                  <span className="text-emerald-600 dark:text-emerald-400 font-medium truncate">
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
                  onClick={() => setTheme((th) => (th === 'dark' ? 'light' : 'dark'))}
                  aria-label={theme === 'dark' ? t.header.themeLight : t.header.themeDark}
                  className="px-0 justify-center"
                >
                  {theme === 'dark' ? (
                    <Sun className="w-3.5 h-3.5 text-amber-400 shrink-0" />
                  ) : (
                    <Moon className="w-3.5 h-3.5 text-zinc-700 shrink-0" />
                  )}
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
                  onClick={() => setTheme((th) => (th === 'dark' ? 'light' : 'dark'))}
                  aria-label={theme === 'dark' ? t.header.themeLight : t.header.themeDark}
                  title={theme === 'dark' ? t.header.themeLight : t.header.themeDark}
                  className="px-2.5 shrink-0"
                >
                  {theme === 'dark' ? (
                    <>
                      <Sun className="w-3.5 h-3.5 text-amber-400 shrink-0" />
                      <span className="truncate max-w-[72px]">{t.header.themeLight}</span>
                    </>
                  ) : (
                    <>
                      <Moon className="w-3.5 h-3.5 text-zinc-700 shrink-0" />
                      <span className="truncate max-w-[72px]">{t.header.themeDark}</span>
                    </>
                  )}
                </Button>
              </div>
            )}
          </div>
          </CustomScrollArea>
        </motion.aside>

        {/* Custom iOS / macOS Interactive Floating Window Scrollbar */}
        <WindowCustomScrollbar />

        {/* Main Content Viewport: Persistent, velocity-preserving iOS spring (zero unmount flash, 100% mid-flight interruptible) */}
        <main className="flex-1 min-w-0 px-3.5 sm:px-6 lg:px-8 pt-4 sm:pt-7 pb-24 md:pb-8 max-w-5xl mx-auto w-full overflow-x-hidden">
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
                connectionStatus={connectionStatus}
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
                onCancelTransfer={handleCancelTransfer}
                onClearCompletedTransfers={handleClearCompletedTransfers}
                coverVersion={coverVersion}
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
                connectionStatus={connectionStatus}
                onUploadFiles={handleUploadFiles}
                onCreateFolder={handleCreateFolder}
                onRenameMoveItem={handleRenameMoveItem}
                onBatchMoveItems={handleBatchMoveItems}
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
                onSetCover={handleSetCover}
                onRemoveCover={handleRemoveCover}
              />
            )}

            {activeTab === 'settings' && (
              <div className="space-y-8">
                <SettingsView
                  t={t}
                  locale={locale}
                  settings={settings}
                  canAutostart={caps.autostart}
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
                onRefresh={() => void reloadDownloads()}
                onOpenDir={handleOpenDownloads}
                onRemove={handleRemoveDownload}
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
          <div className="grid grid-cols-5 gap-1 max-w-md mx-auto">
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
                    setActiveTab(item.id);
                  }}
                  aria-current={isActive ? 'page' : undefined}
                  className={`relative flex flex-col items-center justify-center py-1.5 px-1 rounded-xl text-[11px] font-medium select-none transition-colors z-10 ${
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

      {/* iOS Dynamic Toast Notification Stack */}
      <div
        aria-live="polite"
        className="fixed bottom-20 md:bottom-4 end-4 z-50 flex flex-col gap-2 max-w-md w-[calc(100vw-2rem)] sm:w-auto pointer-events-none"
      >
        <AnimatePresence>
          {toasts.map((toast) => (
            <motion.div
              key={toast.id}
              layout
              initial={{ opacity: 0, y: 16, scale: 0.94 }}
              animate={{ opacity: 1, y: 0, scale: 1 }}
              exit={{ opacity: 0, scale: 0.94, transition: { duration: 0.15 } }}
              transition={IOS_SPRING}
              className={`pointer-events-auto flex items-center justify-between gap-3 px-4 py-2.5 rounded-2xl shadow-lg border text-xs font-medium backdrop-blur-xl ${
                toast.type === 'error'
                  ? 'bg-red-950/90 text-red-100 border-red-800/80'
                  : 'bg-zinc-900/90 text-zinc-100 border-white/10'
              }`}
            >
              <div className="flex items-center gap-2.5">
                {toast.type === 'error' ? (
                  <AlertCircle className="w-4 h-4 text-red-400 shrink-0" />
                ) : (
                  <CheckCircle2 className="w-4 h-4 text-emerald-400 shrink-0" />
                )}
                <span className="leading-relaxed">{toast.text}</span>
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
