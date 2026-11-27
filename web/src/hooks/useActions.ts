import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { DownloadItem, DownloadTask, FileItem, Locale, NavTab } from '../types';
import {
  attachVolume,
  cancelDownload,
  createVolume,
  deleteVolume,
  getDownloads,
  localFileURL,
  mkdir,
  moveItems,
  openDownloadedFile,
  openDownloads,
  pauseDownload,
  pickCoverNative,
  rebuildVolume,
  removeDownload,
  removeItems,
  resumeDownload,
  setCover,
  setVolumeTitle,
  startDownload,
} from '../api';
import { inferFileCategory } from '../utils/fsLogic';
import { interpolate, type Translations } from '../utils/i18n';
import { errText } from '../utils/errorText';
import { exportNativeFile, platformCaps } from '../platform';

type PushToast = (
  text: string,
  type?: 'success' | 'error' | 'pending',
  action?: { label: string; onClick: () => void }
) => string;

type DismissToast = (id: string) => void;

/**
 * 远端操作的统一反馈：先弹 pending 旋转条，成功换对勾，失败换错误。
 * 输入：进行中文案、成功文案、执行体（内部含 reload）、两者回调。
 * 输出：执行体的返回值（供需要结果的调用方使用）。
 * 预期：任何路径下 pending 条都被消掉，不留永久转圈；失败仍走 errText。
 */
async function withFeedback<T>(
  doing: string,
  done: string,
  run: () => Promise<T>,
  pushToast: PushToast,
  dismissToast: DismissToast
): Promise<T> {
  const pending = pushToast(doing, 'pending');
  try {
    const result = await run();
    dismissToast(pending);
    pushToast(done, 'success');
    return result;
  } catch (err) {
    dismissToast(pending);
    pushToast(errText(err), 'error');
    throw err;
  }
}

export function useDownloads(p: {
  activeTab: NavTab;
  t: Translations;
  locale: Locale;
  pushToast: PushToast;
  dismissToast: DismissToast;
  setProblem: (key: string, text: string) => void;
  clearProblem: (key: string) => void;
  localPreview: DownloadItem | null;
  setLocalPreview: (item: DownloadItem | null) => void;
}) {
  const { activeTab, t, locale, pushToast, dismissToast, setProblem, clearProblem, localPreview, setLocalPreview } = p;
  const [downloads, setDownloads] = useState<DownloadItem[]>([]);
  const [downloadTasks, setDownloadTasks] = useState<DownloadTask[]>([]);
  const [downloadsDir, setDownloadsDir] = useState('');

  const reloadDownloads = useCallback(async () => {
    if (!platformCaps.downloads) return;
    try {
      const res = await getDownloads();
      setDownloads(res.files ?? []);
      setDownloadTasks(res.tasks ?? []);
      setDownloadsDir(res.dir ?? '');
      clearProblem('downloads');
    } catch (err) {
      setProblem('downloads', interpolate(t.errors.downloads, { message: errText(err) }, locale));
    }
  }, [clearProblem, locale, setProblem, t]);

  // 切到下载页时拉一次，避免在其它页面反复扫磁盘
  useEffect(() => {
    if (activeTab === 'downloads') void reloadDownloads();
  }, [activeTab, reloadDownloads]);

  const startFileDownload = (file: FileItem) => {
    void withFeedback(
      interpolate(t.downloads.starting, { name: file.name }, locale),
      interpolate(t.downloads.startedToast, { name: file.name }, locale),
      async () => {
        const res = await startDownload(file.volumeId, file.id);
        setDownloadTasks((prev) => [...prev.filter((item) => item.id !== res.task.id), res.task]);
        await reloadDownloads();
      },
      pushToast,
      dismissToast
    ).catch(() => undefined);
  };

  const controlTask = (id: string, action: 'pause' | 'resume' | 'cancel') => {
    const call = action === 'pause' ? pauseDownload(id) : action === 'resume' ? resumeDownload(id) : cancelDownload(id);
    const doing = action === 'pause' ? t.downloads.pausing : action === 'resume' ? t.downloads.resuming : t.downloads.cancelling;
    const done = action === 'pause' ? t.downloads.paused : action === 'resume' ? t.downloads.resumed : t.downloads.cancelled;
    void withFeedback(doing, done, async () => { await call; await reloadDownloads(); }, pushToast, dismissToast)
      .catch(() => undefined);
  };

  const removeFile = (name: string) => {
    removeDownload(name)
      .then(() => reloadDownloads())
      .then(() => pushToast(t.downloads.removedToast, 'success'))
      .catch((err) => pushToast(errText(err), 'error'));
  };

  const openDir = () => {
    openDownloads()
      .then(() => undefined)
      .catch((err) => pushToast(`${t.downloads.openFailedToast}：${errText(err)}`, 'error'));
  };

  // 安卓专用：把沙盒里的下载文件导出到系统公共下载目录
  // names 有值时只导出选中的文件；为空表示导出全部
  const exportDir = (names?: string[]) => {
    const picked = names && names.length > 0 ? new Set(names) : null;
    const targets = downloads.filter((f) => f.path && (!picked || picked.has(f.name)));
    if (targets.length === 0) return pushToast(t.downloads.exportEmptyToast, 'error');
    let ok = 0;
    for (const f of targets) {
      try {
        if (exportNativeFile(f.path)) ok += 1;
      } catch {
        // 桥不可用或某个文件被占用：继续导出剩下的
      }
    }
    if (ok === 0) return pushToast(t.downloads.exportFailedToast, 'error');
    pushToast(t.downloads.exportedToast.replace('{count}', String(ok)), 'success');
  };

  // 本地文件复用同一个预览弹窗：媒体源指向 /api/downloads/file，不经过卷索引
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

  const previewDownloaded = (name: string) => {
    const item = downloads.find((f) => f.name === name);
    if (item) setLocalPreview(item);
  };

  const openFile = (name: string) =>
    openDownloadedFile(name)
      .then(() => undefined)
      .catch((err) => pushToast(errText(err), 'error'));

  return {
    downloads, downloadTasks, downloadsDir, reloadDownloads, downloadedPreview,
    startFileDownload, controlTask, removeFile, openDir, exportDir, previewDownloaded, openFile,
  };
}

export function useFileActions(p: {
  files: FileItem[];
  t: Translations;
  locale: Locale;
  pushToast: PushToast;
  dismissToast: DismissToast;
  reloadFiles: (names?: string[]) => Promise<void>;
}) {
  const { files, t, locale, pushToast, dismissToast, reloadFiles } = p;

  // groupByVolume 按条目所属卷分组，避免批量操作只认第一条的卷
  const groupByVolume = (itemIds: string[]) => {
    const byVol = new Map<string, string[]>();
    for (const id of itemIds) {
      const vol = files.find((f) => f.id === id)?.volumeId;
      if (vol) byVol.set(vol, [...(byVol.get(vol) ?? []), id]);
    }
    return byVol;
  };

  const handleCreateFolder = (volumeId: string, parentId: string | null, name: string) => {
    void withFeedback(
      interpolate(t.files.creatingFolder, { name }, locale),
      interpolate(t.files.folderCreatedToast, { name }, locale),
      async () => { await mkdir(volumeId, parentId || '/', name); await reloadFiles([volumeId]); },
      pushToast,
      dismissToast
    ).catch(() => undefined);
  };

  // 改名/移动：同一父目录只改末段，跨目录用目标目录 + 新名
  const handleRenameMoveItem = (itemId: string, newName: string, targetParentId: string | null) => {
    const item = files.find((f) => f.id === itemId);
    // 列表已经刷新掉这一行时不能静默返回：用户点了保存却什么都不发生等于假按钮
    if (!item) {
      void reloadFiles();
      return pushToast(t.files.itemMissingToast, 'error');
    }
    const targetDir = targetParentId || '/';
    const dest = targetDir === '/' ? `/${newName}` : `${targetDir}/${newName}`;
    void withFeedback(
      t.files.savingChange,
      t.files.itemMovedRenamedToast,
      async () => { await moveItems(item.volumeId, [itemId], targetDir, dest); await reloadFiles([item.volumeId]); },
      pushToast,
      dismissToast
    ).catch(() => undefined);
  };

  const handleBatchMoveItems = (itemIds: string[], targetParentId: string | null) => {
    const byVol = groupByVolume(itemIds);
    if (byVol.size === 0) {
      void reloadFiles();
      return pushToast(t.files.itemMissingToast, 'error');
    }
    void withFeedback(
      t.files.savingChange,
      t.files.itemMovedRenamedToast,
      async () => {
        await Promise.all(Array.from(byVol.entries()).map(([vol, ids]) => moveItems(vol, ids, targetParentId || '/')));
        await reloadFiles([...byVol.keys()]);
      },
      pushToast,
      dismissToast
    ).catch(() => undefined);
  };

  const handleDeleteItems = (itemIds: string[]) => {
    const byVol = groupByVolume(itemIds);
    if (byVol.size === 0) {
      void reloadFiles();
      return pushToast(t.files.itemMissingToast, 'error');
    }
    void withFeedback(
      t.files.deletingItems,
      t.files.deletedToast,
      async () => {
        await Promise.all(Array.from(byVol.entries()).map(([vol, ids]) => removeItems(vol, ids)));
        await reloadFiles([...byVol.keys()]);
      },
      pushToast,
      dismissToast
    ).catch(() => undefined);
  };

  return { handleCreateFolder, handleRenameMoveItem, handleBatchMoveItems, handleDeleteItems };
}

export function useVolumeActions(p: {
  t: Translations;
  locale: Locale;
  pushToast: PushToast;
  dismissToast: (id: string) => void;
  reloadState: () => Promise<string[] | null>;
  reloadFiles: (names?: string[]) => Promise<void>;
}) {
  const { t, locale, pushToast, dismissToast, reloadState, reloadFiles } = p;
  const longOps = useRef(new Map<string, AbortController>());

  // 建频道要十几秒：点击确认立刻弹「正在创建」并带取消按钮；取消会断开请求
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
          // 断开请求后服务端可能已经把卷建完：以卷列表为准
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

  // 封面/标题失败时弹窗保持打开，用户可换一张或继续改（withFeedback 会 rethrow）
  const handleSetCover = (volume: string, file: File): Promise<void> =>
    withFeedback(t.volume.savingCover, t.volume.coverSaved, async () => {
      await setCover(volume, file);
      await reloadState();
    }, pushToast, dismissToast);

  const handlePickCoverNative = async (volume: string): Promise<void> => {
    // 用户取消系统选图：没有远端操作，不弹进度
    if (!(await pickCoverNative(volume))) return;
    await withFeedback(t.volume.savingCover, t.volume.coverSaved, async () => {
      await reloadState();
    }, pushToast, dismissToast);
  };

  const handleSetVolumeTitle = (volume: string, title: string): Promise<void> =>
    withFeedback(t.volume.savingTitle, t.volume.titleSaved, async () => {
      await setVolumeTitle(volume, title);
      await reloadState();
    }, pushToast, dismissToast);

  // 全量翻历史要几十秒：先弹「正在重建」并带取消按钮
  const handleRebuildVolume = async (volume: string): Promise<void> => {
    const ctrl = new AbortController();
    const key = `rebuild:${volume}`;
    longOps.current.set(key, ctrl);
    const pending = pushToast(interpolate(t.files.rebuilding, { name: volume }, locale), 'pending', {
      label: t.files.cancel,
      onClick: () => ctrl.abort(),
    });
    try {
      const res = await rebuildVolume(volume, ctrl.signal);
      await reloadState();
      await reloadFiles([volume]);
      dismissToast(pending);
      pushToast(interpolate(t.files.rebuildDone, { count: res.files }, locale), 'success');
    } catch (err) {
      dismissToast(pending);
      if (ctrl.signal.aborted) pushToast(interpolate(t.files.rebuildCancelled, { name: volume }, locale), 'success');
      else pushToast(errText(err), 'error');
    } finally {
      longOps.current.delete(key);
    }
  };

  return {
    handleCreateVolume,
    handleAttachVolume,
    handleDeleteVolume,
    handleSetCover,
    handlePickCoverNative,
    handleSetVolumeTitle,
    handleRebuildVolume,
  };
}
