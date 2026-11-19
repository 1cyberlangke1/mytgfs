import { useCallback, useEffect, useRef, useState, type MutableRefObject } from 'react';
import type { AppSettings, ConnectionStatus, FileCategory, FileItem, Locale, TransferTask, VolumeInfo } from '../types';
import { getSettings, getState, listFiles, type ApiFile, type ApiQueueItem, type ApiState, type ApiTransfer } from '../api';
import { inferFileCategory, pickActiveVolume } from '../utils/fsLogic';
import { interpolate, type Translations } from '../utils/i18n';
import { errText } from '../utils/errorText';
import { subscribePush } from '../events';
import { STORAGE_KEYS, loadFromStorage, urlParam } from '../utils/storage';

const EMPTY_SETTINGS: AppSettings = { schema: [], values: {} };

// toFileItem 把服务端条目转成前端展示结构。
const toFileItem = (f: ApiFile): FileItem => ({
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
});

// transferFromServer 把服务端下载进度转成传输任务（路径形如 vol:path）。
function transferFromServer(t: ApiTransfer, index: number): TransferTask {
  const at = t.path.indexOf(':');
  const volumeId = at > 0 ? t.path.slice(0, at) : '';
  const name = at > 0 ? t.path.slice(at + 1) : t.path;
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

// queueFromServer 把服务端队列快照转成传输任务；已完成/失败/取消都按服务端状态显示。
function queueFromServer(q: ApiQueueItem, index: number): TransferTask {
  const raw = q.kind || 'upload';
  const opKind: TransferTask['opKind'] =
    raw === 'upload' || raw === 'copy' || raw === 'download'
      ? raw
      : raw === 'mkdir'
        ? 'mkdir'
        : raw === 'rename'
          ? 'rename'
          : raw === 'remove'
            ? 'remove'
            : 'other';
  const state: TransferTask['state'] =
    q.state === 'done'
      ? 'completed'
      : q.state === 'failed'
        ? 'failed'
        : q.state === 'cancelled'
          ? 'cancelled'
          : q.state === 'running'
            ? 'transferring'
            : 'queued';
  return {
    id: `q-${index}-${q.id}`,
    fileName: q.name.split('/').pop() || q.name,
    volumeId: q.volume || '',
    targetParentId: null,
    direction: opKind === 'download' ? 'download' : 'upload',
    size: q.size ?? 0,
    transferredBytes: q.done ?? 0,
    state,
    speedBps: 0,
    phase: 'remote',
    isChunkedResume: false,
    chunkSize: 0,
    currentChunk: 0,
    totalChunks: 0,
    createdAt: new Date().toISOString(),
    category: inferFileCategory(q.name, false),
    errorMessage: q.error,
    opKind,
    queueVolume: q.volume,
    queueJobId: q.id,
  };
}

const connOf = (st?: ApiState['status']): ConnectionStatus => {
  switch (st?.state_code) {
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

export function usePanelData(p: {
  tRef: MutableRefObject<Translations>;
  localeRef: MutableRefObject<Locale>;
  setProblem: (key: string, text: string) => void;
  clearProblem: (key: string) => void;
  reloadDownloads: () => Promise<void>;
}) {
  const { tRef, localeRef, setProblem, clearProblem, reloadDownloads } = p;
  const initialVol = urlParam('vol') || loadFromStorage<string>(STORAGE_KEYS.activeVol, '');
  const [volumes, setVolumes] = useState<VolumeInfo[]>([]);
  const [files, setFiles] = useState<FileItem[]>([]);
  const [filesLoading, setFilesLoading] = useState(true);
  const [serverTransfers, setServerTransfers] = useState<TransferTask[]>([]);
  const [serverQueue, setServerQueue] = useState<TransferTask[]>([]);
  const [settings, setSettings] = useState<AppSettings>(EMPTY_SETTINGS);
  const [connectionStatus, setConnectionStatus] = useState<ConnectionStatus>('connecting');
  const [caps, setCaps] = useState({
    createVolume: false,
    deleteVolume: false,
    attachVolume: false,
    listChannels: false,
    login: false,
    autostart: false,
    cover: false,
    rebuild: false,
  });
  const [loginReady, setLoginReady] = useState<boolean | null>(null);
  const [activeVolumeId, setActiveVolumeId] = useState<string>(initialVol);
  const [currentFolderId, setCurrentFolderId] = useState<string | null>(null);

  const filesLoadedRef = useRef(false);
  const volumesRef = useRef<VolumeInfo[]>([]);
  const preferredVolume = useRef<string>(initialVol);
  const connRef = useRef<ConnectionStatus>('connecting');
  const onConnectedRef = useRef<() => void>(() => {});
  const downloadSpeed = useRef(new Map<string, { bytes: number; at: number }>());
  const restoredFolder = useRef<string | null>(null);

  const selectVolume = useCallback((id: string) => {
    preferredVolume.current = id;
    setActiveVolumeId(id);
  }, []);

  // 速度只看两次采样的字节差；stream 读取不算队列任务
  const sampleDownloads = useCallback((items: ApiTransfer[]): TransferTask[] => {
    const now = performance.now();
    const seen = new Set<string>();
    const out = items
      .filter((t) => t.kind !== 'stream')
      .map((t, i) => {
        const task = transferFromServer(t, i);
        seen.add(task.id);
        const prev = downloadSpeed.current.get(task.id);
        if (prev && now > prev.at && task.transferredBytes >= prev.bytes) {
          task.speedBps = ((task.transferredBytes - prev.bytes) * 1000) / (now - prev.at);
        }
        downloadSpeed.current.set(task.id, { bytes: task.transferredBytes, at: now });
        return task;
      });
    for (const id of downloadSpeed.current.keys()) if (!seen.has(id)) downloadSpeed.current.delete(id);
    return out;
  }, []);

  // reloadState 刷新卷摘要、能力位、连接状态与传输进度；失败写常驻横幅
  const reloadState = useCallback(async (): Promise<string[] | null> => {
    try {
      const st = await getState();
      const names = st.volumes.map((v) => v.name);
      setVolumes(st.volumes.map((v) => ({ id: v.name, name: v.name, title: v.title ?? '', cover: v.cover ?? '' })));
      setActiveVolumeId((cur) => pickActiveVolume(preferredVolume.current, cur, names));
      // 非已连接 → 已连接的跃迁：断线期间错过的变化立刻补拉
      const next = connOf(st.status);
      if (connRef.current !== 'connected' && next === 'connected') onConnectedRef.current();
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
      setLoginReady(typeof st.login_ready === 'boolean' ? st.login_ready : null);
      clearProblem('state');
      return names;
    } catch (err) {
      setProblem('state', interpolate(tRef.current.errors.state, { message: errText(err) }, localeRef.current));
      return null;
    }
  }, [sampleDownloads, setProblem, clearProblem]);

  useEffect(() => {
    volumesRef.current = volumes;
  }, [volumes]);

  // reloadFiles 拉全部卷的完整条目（读本地索引，不进 Telegram）
  const reloadFiles = useCallback(
    async (names?: string[]) => {
      const targets = names ?? volumesRef.current.map((v) => v.id);
      if (targets.length === 0) {
        setFilesLoading(false);
        return;
      }
      // 骨架只在首次加载时占位，刷新不再重插
      if (!filesLoadedRef.current) setFilesLoading(true);
      const results = await Promise.all(
        targets.map(async (name): Promise<ApiFile[] | null> => {
          try {
            const list = await listFiles(name, true);
            clearProblem(`files:${name}`);
            return list;
          } catch (err) {
            setProblem(
              `files:${name}`,
              interpolate(tRef.current.errors.files, { volume: name, message: errText(err) }, localeRef.current)
            );
            return null;
          }
        })
      );
      const fresh = new Map<string, ApiFile[]>();
      const failed = new Set<string>();
      targets.forEach((name, i) => {
        const list = results[i];
        if (list) fresh.set(name, list);
        else failed.add(name);
      });
      // 单卷刷新时其它卷原样保留；全量刷新时只保留读失败的卷，已删卷随之消失
      const keepOld = (vol: string) => (fresh.has(vol) ? false : names ? true : failed.has(vol));
      setFiles((prev) => [...prev.filter((f) => keepOld(f.volumeId)), ...Array.from(fresh.values()).flat().map(toFileItem)]);
      filesLoadedRef.current = true;
      setFilesLoading(false);
    },
    [setProblem, clearProblem]
  );

  const reloadSettings = useCallback(async () => {
    try {
      const s = await getSettings();
      setSettings((prev) => ({ ...prev, schema: s.schema ?? [], values: s.values ?? {} }));
      clearProblem('settings');
    } catch (err) {
      setProblem('settings', interpolate(tRef.current.errors.settings, { message: errText(err) }, localeRef.current));
    }
  }, [setProblem, clearProblem]);

  // 事件驱动：服务端有变化才推送，前端收到后拉最新快照。突发合并用 trailing 定时器。
  useEffect(() => {
    const throttle = (ms: number, run: () => void) => {
      let timer = 0;
      let trailing = false;
      const fire = () => {
        if (timer !== 0) {
          trailing = true;
          return;
        }
        run();
        timer = window.setTimeout(() => {
          timer = 0;
          if (trailing) {
            trailing = false;
            fire();
          }
        }, ms);
      };
      return { fire, stop: () => window.clearTimeout(timer) };
    };
    const state = throttle(400, () => void reloadState());
    const fileR = throttle(1000, () => void reloadFiles());
    const dl = throttle(400, () => void reloadDownloads());
    const refreshAll = () => {
      state.fire();
      fileR.fire();
      dl.fire();
    };
    onConnectedRef.current = refreshAll;

    const unsubscribe = subscribePush(
      {
        status: () => {
          clearProblem('events');
          refreshAll();
        },
        // 进度事件只动字节数：文件树没变，重拉整棵树在大卷上纯属白烧
        progress: () => {
          clearProblem('events');
          state.fire();
          dl.fire();
        },
        config: () => {
          void reloadSettings();
          refreshAll();
        },
      },
      {
        onConnect: refreshAll,
        // 断线时浏览器会一直静默重连；不提示的话用户会以为数据是实时的
        onError: () => setProblem('events', tRef.current.errors.connection),
      }
    );

    // 先订阅再首拉：订阅前发生的变化由这次拉取覆盖
    void (async () => {
      const names = await reloadState();
      if (names && names.length > 0) await reloadFiles(names);
      await reloadSettings();
    })();

    // 窗口重新可见 / 重新聚焦：宿主 webview 可能晚于挂载才显示，补一次状态
    const onVisible = () => {
      if (document.visibilityState !== 'visible') return;
      state.fire();
      fileR.fire();
    };
    window.addEventListener('focus', onVisible);
    document.addEventListener('visibilitychange', onVisible);

    return () => {
      state.stop();
      fileR.stop();
      dl.stop();
      window.removeEventListener('focus', onVisible);
      document.removeEventListener('visibilitychange', onVisible);
      unsubscribe();
    };
  }, [reloadState, reloadFiles, reloadSettings]);

  // 记住每个卷上次停留的目录：刷新后回到原处；目录已被删则回根。
  useEffect(() => {
    if (!activeVolumeId || files.length === 0) return;
    if (restoredFolder.current === activeVolumeId) return;
    restoredFolder.current = activeVolumeId;
    const saved = loadFromStorage<string | null>(`mytgfs_folder_${activeVolumeId}_v1`, null);
    if (saved && files.some((f) => f.id === saved && f.isDir && f.volumeId === activeVolumeId)) setCurrentFolderId(saved);
  }, [activeVolumeId, files]);

  useEffect(() => {
    // 恢复完成前不要用初始的 null 覆盖已保存的目录
    if (!activeVolumeId || restoredFolder.current !== activeVolumeId) return;
    try {
      window.localStorage.setItem(`mytgfs_folder_${activeVolumeId}_v1`, JSON.stringify(currentFolderId));
    } catch {
      // 隐私模式禁用存储时忽略
    }
  }, [activeVolumeId, currentFolderId]);

  return {
    volumes,
    files,
    filesLoading,
    settings,
    setSettings,
    caps,
    loginReady,
    connectionStatus,
    serverTransfers,
    serverQueue,
    setServerQueue,
    activeVolumeId,
    selectVolume,
    currentFolderId,
    setCurrentFolderId,
    reloadState,
    reloadFiles,
    reloadSettings,
    onConnectedRef,
  };
}
